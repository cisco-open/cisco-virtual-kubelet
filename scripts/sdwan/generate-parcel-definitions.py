#!/usr/bin/env python3
"""Generate native scalar parcel mappings from the pinned module/provider sources.

Only reviewed resources with scalar fields and simple object lists are accepted. Unsupported syntax fails the
build; the generator does not guess mappings or silently omit module attributes.
Generated metadata derives from the MPL-2.0 provider (see definitions/LICENSE).
"""
import argparse
import json
from pathlib import Path
import re
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("module", type=Path)
parser.add_argument("provider", type=Path)
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
lock = json.loads((root / "internal/controlleradapter/sdwan/testdata/upstream-lock.json").read_text())
for key, path in [("module", args.module), ("provider", args.provider)]:
    revision = subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()
    if revision != lock[key]["commit"]:
        raise SystemExit(f"{key} revision does not match the lock")

def block(text, start):
    depth = 1
    pos = start
    while depth:
        pos += 1
        depth += (text[pos] == "{") - (text[pos] == "}")
    return text[start + 1:pos]

def put(obj, path, value):
    parts = path.split(".")
    for part in parts[:-1]:
        obj = obj.setdefault(part, {})
    obj[parts[-1]] = value

def validate_schema(schema):
    known = {"type", "enum", "minimum", "maximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "items", "anyOf", "oneOf", "uniqueItems", "default", "description", "additionalProperties", "x-attributeMetadata", "format"}
    if set(schema) - known:
        raise SystemExit(f"Unsupported validation keywords: {set(schema) - known}")
    if schema.get("format") not in (None, "ipv6", "hostname"):
        raise SystemExit("Unsupported schema format")
    if "items" in schema:
        validate_schema(schema["items"])
    for item in schema.get("anyOf", []) + schema.get("oneOf", []):
        validate_schema(item)

def definition(source, alias, indent, model, body, schema, receiver="data", target="body"):
    fields = {tf: (name, typ) for name, typ, tf in re.findall(r'(\w+)\s+types\.(\w+)\s+`tfsdk:"([^"]+)"`', model)}
    prefix = r'path\+' if target == "body" else ''
    setter = r'sjson.Set\(' + target + ', ' + prefix
    baseline, inputs, lists = {}, {}, {}
    for match in re.finditer(r'if ' + receiver + r'\.(\w+)\.IsNull\(\) \{', body):
        content = block(body, match.end() - 1)
        for path, literal in re.findall(setter + r'"([^"]+)", ([^\n]+)\)', content):
            put(baseline, path, json.loads(literal))
    assignments = re.findall('^' + ' ' * indent + r'(\w+)\s*=\s*(.*)$', source, re.M)
    for tf, expr in assignments:
        if tf in {"for_each", "name", "description", "feature_profile_id"}:
            continue
        match = re.fullmatch(r'try\(' + re.escape(alias) + r'\.(\w+), null\)', expr)
        variable = re.fullmatch(r'try\("\{\{\$\{' + re.escape(alias) + r'\.(\w+)\}\}\}", null\)', expr)
        if match or variable:
            inputs[tf] = (match or variable).group(1)
            continue
        array = re.search(r'\[for (\w+) in ' + re.escape(alias) + r'\.(\w+) : \{', expr)
        if not array:
            raise SystemExit(f"Unsupported expression {tf}: {expr}")
        item_alias, public = array.groups()
        item_source = block(source, source.index("{", source.index(expr) + array.start()))
        go_name = re.search(r'(\w+)\s+\[\]\w+\s+`tfsdk:"' + tf + '"`', model).group(1)
        loop = re.search(r'for _, item := range ' + receiver + r'\.' + go_name + r' \{', body)
        item_body = block(body, loop.end() - 1)
        array_path = re.findall(setter + r'"([^"]+)", \[\]interface\{\}\{\}\)', body[:loop.start()])[-1]
        node = schema
        for part in array_path.split("."):
            node = node["properties"][part]
        lists[public] = {"path": array_path, "definition": definition(item_source, item_alias, indent + 2, model, item_body, node["items"], "item", "itemBody")}
        put(baseline, array_path, [])
    mapping = {}
    for tf, public in inputs.items():
        if tf.endswith("_variable"):
            continue
        name, typ = fields[tf]
        paths = re.findall(setter + r'"([^"]+)\.value", ' + receiver + r'\.' + name + r'\.Value\w+\(\)\)', body)
        if typ == "Set":
            match = re.search(receiver + r'\.' + name + r'\.ElementsAs[^\n]*\n\s*' + target + ', _ = ' + setter + r'"([^"]+)\.value", values\)', body)
            paths = [match.group(1)] if match else []
        if len(set(paths)) != 1:
            raise SystemExit(f"Ambiguous or missing API path {tf}")
        path = paths[0]
        node = schema
        for part in path.split("."):
            node = node["properties"][part]
        global_schema = next(x["properties"]["value"] for x in node["oneOf"] if x["properties"]["optionType"]["enum"] == ["global"])
        validate_schema(global_schema)
        mapping[public] = {"path": path, "type": typ, "schema": global_schema}
        if tf + "_variable" in inputs:
            mapping[public]["variable"] = inputs[tf + "_variable"]
    return {"defaults": baseline, "fields": mapping, "lists": lists}

hcl = (args.module / "sdwan_features_system.tf").read_text()
output = {"license": "MPL-2.0", "provider_commit": lock["provider"]["commit"], "module_commit": lock["module"]["commit"], "parcels": {}}
for feature in ["global", "omp", "basic", "ntp", "logging"]:
    resource = "sdwan_system_" + feature + "_feature"
    start = hcl.index("{", hcl.index('resource "' + resource + '"'))
    source = block(hcl, start)
    model = (args.provider / "internal/provider" / ("model_" + resource + ".go")).read_text()
    body = model[model.index("toBody(ctx"):model.index("return body")]
    schema = json.loads((args.provider / "gen/models/profile_parcels" / ("system_" + feature + ".json")).read_text())["request"]["properties"]["data"]
    output["parcels"][feature] = definition(source, "each.value." + feature, 2, model, body, schema)
folder = root / "internal/controlleradapter/sdwan/definitions"
folder.mkdir(exist_ok=True)
(folder / "scalar-parcels.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n")
(folder / "LICENSE").write_text((args.provider / "LICENSE").read_text())
