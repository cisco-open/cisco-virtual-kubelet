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
import yaml

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

module_defaults = yaml.safe_load((args.module / "defaults/defaults.yaml").read_text())

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
    known = {"const", "type", "enum", "minimum", "maximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "items", "anyOf", "oneOf", "uniqueItems", "default", "description", "additionalProperties", "x-attributeMetadata", "format"}
    if set(schema) - known:
        raise SystemExit(f"Unsupported validation keywords: {set(schema) - known}")
    if schema.get("format") not in (None, "ipv4", "ipv6", "hostname"):
        raise SystemExit("Unsupported schema format")
    if "items" in schema:
        validate_schema(schema["items"])
    for item in schema.get("anyOf", []) + schema.get("oneOf", []):
        validate_schema(item)

def property_schema(node, name):
    if name in node.get("properties", {}):
        return node["properties"][name]
    candidates = [branch["properties"][name] for branch in node.get("oneOf", [])
                  if name in branch.get("properties", {})]
    if len(candidates) > 1 and all(x.get("properties", {}).get("optionType", {}).get("enum") == ["global"] for x in candidates):
        return {"oneOf": candidates}
    if len(candidates) != 1:
        raise SystemExit(f"Ambiguous or missing schema property {name}")
    return candidates[0]

def definition(source, alias, indent, model, body, schema, receiver="data", target="body"):
    fields = {tf: (name, typ) for name, typ, tf in re.findall(r'(\w+)\s+types\.(\w+)\s+`tfsdk:"([^"]+)"`', model)}
    prefix = r'path\+' if target == "body" else ''
    setter = r'sjson.Set\(' + target + ', ' + prefix
    baseline, inputs, lists, required, public_defaults, wrappers = {}, {}, {}, [], {}, []
    for match in re.finditer(r'if ' + receiver + r'\.(\w+)\.IsNull\(\) \{', body):
        content = block(body, match.end() - 1)
        for path, literal in re.findall(setter + r'"([^"]+)", ([^\n]+)\)', content):
            put(baseline, path, json.loads(literal))
    assignments = re.findall('^' + ' ' * indent + r'(\w+)\s*=\s*(.*)$', source, re.M)
    for tf, expr in assignments:
        if indent == 2 and tf in {"for_each", "name", "description", "feature_profile_id"}:
            continue
        match = re.fullmatch(r'try\(' + re.escape(alias) + r'\.(\w+), null\)', expr)
        fallback = re.fullmatch(r'try\(' + re.escape(alias) + r'\.(\w+), local.defaults.([\w.]+), null\)', expr)
        if fallback:
            public, default_path = fallback.groups()
            value = module_defaults["defaults"]
            for part in default_path.split("."):
                value = value[part]
            public_defaults[public] = value
            inputs[tf] = public
            continue
        variable = re.fullmatch(r'try\("\{\{\$\{' + re.escape(alias) + r'\.(\w+)\}\}\}", null\)', expr)
        if match or variable:
            inputs[tf] = (match or variable).group(1)
            continue
        direct = re.fullmatch(re.escape(alias) + r'\.(\w+)', expr)
        if direct:
            inputs[tf] = direct.group(1)
            required.append(direct.group(1))
            continue
        array = re.search(r'\[for (\w+) in ' + re.escape(alias) + r'\.(\w+) : \{', expr)
        if not array and expr != "[{":
            raise SystemExit(f"Unsupported expression {tf}: {expr}")
        item_alias, public = array.groups() if array else (alias, None)
        item_source = block(source, source.index("{", source.index(expr) + (array.start() if array else 0)))
        go_name = re.search(r'(\w+)\s+\[\]\w+\s+`tfsdk:"' + tf + '"`', model).group(1)
        loop = re.search(r'for _, item := range ' + receiver + r'\.' + go_name + r' \{', body)
        item_body = block(body, loop.end() - 1)
        array_paths = re.findall(setter + r'"([^"]+)", \[\]interface\{\}\{\}\)', body[:loop.start()])
        array_path = array_paths[-1] if array_paths else re.findall(r'sjson.SetRaw\(' + target + r', ' + prefix + r'"([^"]+)\.-1", itemBody\)', body)[0]
        node = schema
        for part in array_path.split("."):
            node = property_schema(node, part)
        entry = {"path": array_path, "definition": definition(item_source, item_alias, indent + 2, model, item_body, node["items"], "item", "itemBody")}
        if array:
            lists[public] = entry
        else:
            wrappers.append(entry)
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
            node = property_schema(node, part)
        global_values = [x["properties"]["value"] for x in node.get("oneOf", [node]) if x["properties"]["optionType"]["enum"] == ["global"]]
        if not global_values:
            raise SystemExit(f"Missing global value schema {tf}")
        global_schema = global_values[0] if len(global_values) == 1 else {"anyOf": global_values}
        validate_schema(global_schema)
        mapping[public] = {"path": path, "type": typ, "schema": global_schema}
        if tf + "_variable" in inputs:
            mapping[public]["variable"] = inputs[tf + "_variable"]
    return {"defaults": baseline, "fields": mapping, "lists": lists, "required": required, "public_defaults": public_defaults, "wrappers": wrappers}

output = {"license": "MPL-2.0", "provider_commit": lock["provider"]["commit"], "module_commit": lock["module"]["commit"], "parcels": {}}
reviewed = [("system", f, f, f) for f in ["global", "omp", "basic", "ntp", "logging", "security"]]
reviewed += [("transport", "gps", "gps", "transport_gps"),
             ("other", "trustsec", "trustsec", "other_trustsec"), ("other", "ucse", "ucse", "other_ucse"),
             ("other", "thousandeyes", "thousandeyes", "other_thousandeyes")]
for family, feature, alias, key in reviewed:
    hcl = (args.module / ("sdwan_features_" + family + ".tf")).read_text()
    resource = "sdwan_" + family + "_" + feature + "_feature"
    start = hcl.index("{", hcl.index('resource "' + resource + '"'))
    source = block(hcl, start)
    model = (args.provider / "internal/provider" / ("model_" + resource + ".go")).read_text()
    body = model[model.index("toBody(ctx"):model.index("return body")]
    schema = json.loads((args.provider / "gen/models/profile_parcels" / (family + "_" + feature + ".json")).read_text())["request"]["properties"]["data"]
    output["parcels"][key] = definition(source, "each.value." + alias, 2, model, body, schema)
folder = root / "internal/controlleradapter/sdwan/definitions"
folder.mkdir(exist_ok=True)
(folder / "scalar-parcels.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n")
(folder / "LICENSE").write_text((args.provider / "LICENSE").read_text())
