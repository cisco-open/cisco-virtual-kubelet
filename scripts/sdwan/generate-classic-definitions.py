#!/usr/bin/env python3
"""Generate reviewed scalar classic-template mappings from pinned upstream sources."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import yaml

parser = argparse.ArgumentParser()
parser.add_argument("module", type=Path)
parser.add_argument("provider", type=Path)
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
lock = json.loads((root / "internal/controlleradapter/sdwan/testdata/upstream-lock.json").read_text())
for key, path in [("module", args.module), ("provider", args.provider)]:
    if subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip() != lock[key]["commit"]:
        raise SystemExit("Reference checkout does not match the lock")

def block(text, start):
    depth, pos = 1, start
    while depth:
        pos += 1
        depth += (text[pos] == "{") - (text[pos] == "}")
    return text[start + 1:pos]

def put(obj, path, value):
    parts = path.split(".")
    for part in parts[:-1]:
        obj = obj.setdefault(part, {})
    obj[parts[-1]] = value

def default_setters(content, baseline):
    for path, key, literal in re.findall(r'sjson.Set\(body, path\+"([^"]+)\."\+"([^"]+)", ([^\n]+)\)', content):
        if literal == "[]interface{}{}":
            value = []
        elif literal.startswith("[]string{") and literal.endswith("}"):
            value = json.loads("[" + literal[len("[]string{"):-1] + "]")
        else:
            value = json.loads(literal)
        put(baseline, path + "." + key, value)

hcl = (args.module / "sdwan_feature_templates.tf").read_text()
defaults = yaml.safe_load((args.module / "defaults/defaults.yaml").read_text())["defaults"]["sdwan"]["edge_feature_templates"]
output = {"license": "MPL-2.0", "module_commit": lock["module"]["commit"], "provider_commit": lock["provider"]["commit"], "templates": {}}
for resource in ["sdwan_cedge_global_feature_template", "sdwan_cli_template_feature_template", "sdwan_cellular_controller_feature_template", "sdwan_cellular_cedge_profile_feature_template", "sdwan_cedge_multicast_feature_template"]:
    source = block(hcl, hcl.index("{", hcl.index('resource "' + resource + '"')))
    public_collection = re.search(r'local.edge_feature_templates\.(\w+)', source).group(1)
    model = (args.provider / "internal/provider" / ("model_" + resource + ".go")).read_text()
    body = model[model.index("toBody(ctx"):model.index("return body")]
    baseline = {}
    for match in re.finditer(r'if data\.\w+\.IsNull\(\) \{', body):
        default_setters(block(body, match.end() - 1), baseline)
    for match in re.finditer(r'if len\(data\.\w+\) > 0 \{', body):
        content = block(body, match.end() - 1)
        tail = match.end() + len(content) + 1
        alternative = re.match(r'\s*else \{', body[tail:])
        if not alternative:
            raise SystemExit("Unreviewed classic list default")
        default_setters(block(body, tail + alternative.end() - 1), baseline)
    template_type = re.search(r'"templateType", "([^"]+)"', body).group(1)
    schema = json.loads((args.provider / "gen/models/feature_templates" / (template_type + ".json")).read_text())
    leaves = {".".join(x["dataPath"] + [x["key"]]): x for x in schema["fields"]}
    fields = {tf: (name, typ) for name, typ, tf in re.findall(r'(\w+)\s+types\.(\w+)\s+`tfsdk:"([^"]+)"`', model)}
    inputs, required = {}, []
    for tf, expression in re.findall(r'^  (\w+)\s*=\s*(.*)$', source, re.M):
        if tf in {"for_each", "name", "description", "device_types", "depends_on"}:
            continue
        match = re.fullmatch(r'try\(each.value\.(\w+), null\)', expression)
        direct = re.fullmatch(r'each.value\.(\w+)', expression)
        if not (match or direct):
            raise SystemExit(f"Unreviewed classic expression: {tf}")
        inputs[tf] = (match or direct).group(1)
        if direct:
            required.append(direct.group(1))
    mapping = {}
    for tf, public in inputs.items():
        if tf.endswith("_variable"):
            continue
        name, typ = fields[tf]
        paths = set(re.findall(r'path\+"([^"]+)\."\+"vipValue", (?:strconv.FormatBool\()?data\.' + name + r'\.Value\w+\(\)', body))
        if len(paths) != 1:
            raise SystemExit(f"Ambiguous classic path: {tf}")
        path = paths.pop()
        leaf = leaves[path]
        if leaf["objectType"] not in ("object", "node-only"):
            raise SystemExit("Only reviewed scalar classic leaves are supported")
        datatype = leaf["dataType"]
        if set(datatype) - {"type", "default", "min", "max", "minLength", "maxLength", "values", "pattern"}:
            raise SystemExit("Unreviewed classic value constraints")
        value_schema = {"type": {"String": "string", "Bool": "boolean", "Int64": "integer"}[typ]}
        for before, after in [("min", "minimum"), ("max", "maximum"), ("minLength", "minLength"), ("maxLength", "maxLength"), ("pattern", "pattern")]:
            if before in datatype:
                value_schema[after] = datatype[before]
        if datatype["type"] == "enum":
            value_schema["enum"] = [x["key"] for x in datatype["values"]]
        null = re.search(r'if data\.' + name + r'\.IsNull\(\) \{', body)
        omit = null is None or '"vipType", "ignore"' not in block(body, null.end() - 1)
        mapping[public] = {"path": path, "schema": value_schema, "omit_null": omit, "object_type": leaf["objectType"], "boolean_string": "strconv.FormatBool(data." + name + ".ValueBool())" in body}
        if tf + "_variable" in inputs:
            mapping[public]["variable"] = inputs[tf + "_variable"]
    output["templates"][public_collection] = {"type": template_type, "fields": mapping, "required": required, "defaults": baseline, "device_types": defaults[public_collection]["device_types"]}
(root / "internal/controlleradapter/sdwan/definitions/classic-templates.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n")
