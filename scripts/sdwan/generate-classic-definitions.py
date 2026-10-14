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

def default_setters(content, baseline, target="body", prefix=r"path\+"):
    for path in re.findall(r'sjson.Set\(' + target + ', ' + prefix + r'"([^"]+)", map\[string\]interface\{\}\{\}\)', content):
        node = baseline
        for part in path.split("."):
            node = node.setdefault(part, {})
    for path, key, literal in re.findall(r'sjson.Set\(' + target + ', ' + prefix + r'"([^"]+)\."\+"([^"]+)", ([^\n]+)\)', content):
        if literal == "[]interface{}{}":
            value = []
        elif literal.startswith("[]string{") and literal.endswith("}"):
            value = json.loads("[" + literal[len("[]string{"):-1] + "]")
        else:
            value = json.loads(literal)
        put(baseline, path + "." + key, value)

def definition(source, alias, indent, model, body, schema_fields, receiver="data", target="body", struct_name=None):
    prefix = r"path\+" if target == "body" else ""
    baseline = {}
    for match in re.finditer(r'if ' + receiver + r'\.\w+\.IsNull\(\) \{', body):
        default_setters(block(body, match.end() - 1), baseline, target, prefix)
    for match in re.finditer(r'if len\(' + receiver + r'\.\w+\) > 0 \{', body):
        content = block(body, match.end() - 1)
        tail = match.end() + len(content) + 1
        alternative = re.match(r'\s*else \{', body[tail:])
        if not alternative:
            raise SystemExit("Unreviewed classic list default")
        default_setters(block(body, tail + alternative.end() - 1), baseline, target, prefix)
    leaves = {".".join(x["dataPath"] + [x["key"]]): x for x in schema_fields}
    struct_match = re.search(r"type " + (re.escape(struct_name) if struct_name else r"\w+") + r" struct \{", model)
    struct_body = block(model, struct_match.end()-1)
    fields = {tf: (name, typ) for name, typ, tf in re.findall(r'(\w+)\s+types\.(\w+)\s+`tfsdk:"([^"]+)"`', struct_body)}
    inputs, required, trees, optional, conditions, unavailable, literals, ranges, wrapped, boolean_choices, present, value_maps = {}, [], {}, "", {}, {}, {}, {}, set(), {}, {}, {}
    for tf, expression in re.findall(r'^' + ' ' * indent + r'(\w+)\s*=\s*(.*)$', source, re.M):
        if indent == 2 and tf in {"for_each", "name", "description", "device_types", "depends_on"}:
            continue
        if re.fullmatch(r'"[^"\n]*"|[0-9]+|true|false', expression):
            inputs[tf] = "$literal:" + tf
            literals[tf] = json.loads(expression)
            continue
        if tf == "exclude_addresses" and alias == "each.value":
            expected = 'try(each.value.exclude_addresses, each.value.exclude_addresses_ranges, null) == null ? null : concat(try(each.value.exclude_addresses, []), [for r in try(each.value.exclude_addresses_ranges, []) : "${r.from}-${r.to}"])'
            if expression != expected:
                raise SystemExit("Unreviewed DHCP address ranges")
            inputs[tf] = "exclude_addresses"
            ranges[tf] = "exclude_addresses_ranges"
            continue
        if tf == "authorization" and alias == "c":
            expected = 'try(c.authorization_read_only, can(c.authorization_variable) ? null : local.defaults.sdwan.edge_feature_templates.snmp_templates.communities.authorization_read_only) == true ? "read-only" : null'
            if expression != expected:
                raise SystemExit("SNMP community authorization expression changed")
            inputs[tf] = "authorization_read_only"
            boolean_choices[tf] = "read-only"
            continue
        presence = re.fullmatch(r'try\(' + re.escape(alias) + r'\.(\w+), null\) == null \? null : true', expression)
        if presence:
            inputs[tf] = "$derived:" + tf
            present[tf] = presence.group(1)
            continue
        if tf == "authentication_type" and alias == "prof":
            expected = 'try(prof.authentication_type, null) == "server" ? "Server" : try(prof.authentication_type, null) == "mutual" ? "Mutual" : null'
            if expression != expected:
                raise SystemExit("Logging authentication expression changed")
            inputs[tf] = "authentication_type"
            value_maps[tf] = {"server":"Server", "mutual":"Mutual"}
            continue
        if expression == alias:
            inputs[tf] = "$value"
            required.append("$value")
            continue
        condition = re.fullmatch(re.escape(alias) + r' == "([^"]+)" \? "([^"]+)" : null', expression)
        if condition:
            inputs[tf] = "$derived:" + tf
            conditions[tf] = condition.groups()
            continue
        single = re.fullmatch(r'try\(\[' + re.escape(alias) + r'\.([\w.]+)\], null\)', expression)
        if single:
            inputs[tf] = single.group(1)
            wrapped.add(tf)
            continue
        match = re.fullmatch(r'try\(' + re.escape(alias) + r'\.([\w.]+), null\)', expression)
        direct = re.fullmatch(re.escape(alias) + r'\.(\w+)', expression)
        if tf == "optional" and match:
            if '"vipOptional", ' + receiver + '.Optional.ValueBool()' not in body:
                if alias == "addr" and "data.RpAddresses" in model and "pim.rp-addr" in model:
                    unavailable[match.group(1)] = "Pinned provider does not serialize PIM RP address optional"
                    continue
                raise SystemExit("Unreviewed optional row encoding")
            optional = match.group(1)
            continue
        if match or direct:
            inputs[tf] = (match or direct).group(1)
            if direct:
                required.append(direct.group(1))
            continue
        array = re.search(r'\[for (\w+) in ' + re.escape(alias) + r'\.(\w+) : \{', expression)
        if not array:
            raise SystemExit(f"Unreviewed classic expression: {tf}")
        item_alias, public = array.groups()
        item_source = block(source, source.index("{", source.index(expression) + array.start()))
        go_name, child_struct = re.search(r'(\w+)\s+\[\](\w+)\s+`tfsdk:"' + tf + '"`', struct_body).groups()
        loop = re.search(r'for _, (\w+) := range ' + receiver + r'\.' + go_name + r' \{', body)
        item_body = block(body, loop.end() - 1)
        item_receiver = loop.group(1)
        item_target = re.search(r'(\w+) := ""', item_body).group(1)
        path = re.search(r'sjson.SetRaw\(' + target + ', ' + prefix + r'"([^"]+)\."\+"vipValue\.-1", ' + item_target + r'\)', item_body).group(1)
        leaf = leaves[path]
        child = definition(item_source, item_alias, indent + 2, model, item_body, leaf["children"], item_receiver, item_target, child_struct)
        attribute_variable = re.search(r'(\w+) := make\(\[\]string, 0\)', item_body).group(1)
        child["priority_order"] = re.findall(attribute_variable + r' = append\(' + attribute_variable + r', "([^"]+)"\)', item_body)
        child["always_priority_order"] = bool(re.search(r'^\t\t' + item_target + r', _ = sjson.Set\(' + item_target + r', "priority-order",', item_body, re.M))
        trees[public] = {"path": path, "primary_keys": leaf["primaryKeys"], "definition": child, "primitive": "$value" in child["fields"]}
    mapping = {}
    for tf, public in inputs.items():
        if tf.endswith("_variable"):
            continue
        name, typ = fields[tf]
        paths = set(re.findall(prefix + r'"([^"]+)\."\+"vipValue", (?:strconv.FormatBool\()?' + receiver + r'\.' + name + r'\.Value\w+\(\)', body))
        if typ in ("Set", "List"):
            match = re.search(receiver + r'\.' + name + r'\.ElementsAs[^\n]*\n\s*' + target + r', _ = sjson.Set\(' + target + ', ' + prefix + r'"([^"]+)\."\+"vipValue", values\)', body)
            paths = {match.group(1)} if match else set()
        if len(paths) != 1:
            raise SystemExit(f"Ambiguous classic path: {tf}")
        path = paths.pop()
        leaf = leaves[path]
        if leaf["objectType"] not in ("object", "node-only", "list"):
            raise SystemExit("Unreviewed classic leaf type")
        datatype = leaf["dataType"]
        if datatype["type"] not in ("string", "boolean", "number", "enum", "ipv4", "ipv4-prefix", "ipv4Range", "mac", "radioButtonList", "passphrase", "restrictedPassphrase", "ike", "ip", "enumList") or set(datatype) - {"type", "default", "min", "max", "minLength", "maxLength", "values", "pattern", "label", "range"}:
            raise SystemExit("Unreviewed classic value constraints")
        value_type = {"String": "string", "Bool": "boolean", "Int64": "integer"}.get(typ)
        if typ in ("Set", "List"):
            value_type = "integer" if datatype["type"] == "number" else "string"
        if value_type is None:
            raise SystemExit("Unsupported classic field type")
        value_schema = {"type": value_type}
        for before, after in [("min", "minimum"), ("max", "maximum"), ("minLength", "minLength"), ("maxLength", "maxLength"), ("pattern", "pattern")]:
            if before in datatype:
                value_schema[after] = datatype[before]
        if "range" in datatype:
            alternatives = []
            for part in datatype["range"].split(","):
                if re.fullmatch(r"[0-9]+", part):
                    alternatives.append({"const": int(part)})
                elif re.fullmatch(r"[0-9]+-[0-9]+", part):
                    lo, hi = map(int, part.split("-"))
                    alternatives.append({"minimum": lo, "maximum": hi})
                else:
                    raise SystemExit("Unsupported classic numeric range")
            value_schema["anyOf"] = alternatives
        if datatype["type"] == "ip":
            value_schema["format"] = "ip"
        if datatype["type"] == "ipv4":
            value_schema["format"] = "ipv4"
        if datatype["type"] in ("ipv4Range", "mac"):
            value_schema["format"] = datatype["type"]
        if datatype["type"] == "ipv4-prefix":
            value_schema["format"] = "ipv4-prefix"
        if datatype["type"] == "radioButtonList":
            value_schema["enum"] = [x["value"] for x in datatype["values"]]
        if datatype["type"] in ("enum", "enumList"):
            value_schema["enum"] = [x["key"] for x in datatype["values"]]
        if typ in ("Set", "List"):
            value_schema = {"type": "array", "items": value_schema}
        null = re.search(r'if ' + receiver + r'\.' + name + r'\.IsNull\(\) \{', body)
        omit = null is None or '"vipType", "ignore"' not in block(body, null.end() - 1)
        mapping[public] = {"path": path, "schema": value_schema, "set": typ == "Set", "omit_null": omit, "object_type": leaf["objectType"], "boolean_string": "strconv.FormatBool(" + receiver + "." + name + ".ValueBool())" in body}
        if tf in present:
            mapping[public].update(derived=True, present=present[tf], derived_value=True)
        if tf in value_maps:
            mapping[public].update(values=value_maps[tf], input_schema={"type":"string", "enum":list(value_maps[tf])})
        if tf in boolean_choices:
            mapping[public].update(boolean_choice=boolean_choices[tf], input_default=defaults["snmp_templates"]["communities"]["authorization_read_only"])
        if tf in wrapped:
            mapping[public]["wrap_list"] = True
        if tf in ranges:
            mapping[public]["ranges"] = ranges[tf]
        if tf in literals:
            value = literals[tf]
            if typ == "String":
                value = str(value)
            mapping[public].update(literal=value)
        if tf in conditions:
            mapping[public].update(derived=True, when=conditions[tf][0], derived_value=conditions[tf][1])
        if tf + "_variable" in inputs:
            mapping[public]["variable"] = inputs[tf + "_variable"]
    return {"fields": mapping, "required": required, "defaults": baseline, "trees": trees, "optional": optional, "unavailable_inputs": unavailable}

hcl = (args.module / "sdwan_feature_templates.tf").read_text()
defaults = yaml.safe_load((args.module / "defaults/defaults.yaml").read_text())["defaults"]["sdwan"]["edge_feature_templates"]
output = {"license": "MPL-2.0", "module_commit": lock["module"]["commit"], "provider_commit": lock["provider"]["commit"], "templates": {}}
for resource in ["sdwan_cedge_global_feature_template", "sdwan_cli_template_feature_template", "sdwan_cellular_controller_feature_template", "sdwan_cellular_cedge_profile_feature_template", "sdwan_cedge_multicast_feature_template", "sdwan_cisco_bfd_feature_template", "sdwan_cedge_igmp_feature_template", "sdwan_cisco_ntp_feature_template", "sdwan_cisco_omp_feature_template", "sdwan_cedge_pim_feature_template", "sdwan_cisco_thousandeyes_feature_template", "sdwan_security_app_hosting_feature_template", "sdwan_cisco_dhcp_server_feature_template", "sdwan_cisco_vpn_interface_ipsec_feature_template", "sdwan_cisco_snmp_feature_template", "sdwan_cisco_logging_feature_template"]:
    source = block(hcl, hcl.index("{", hcl.index('resource "' + resource + '"')))
    if resource == "sdwan_cisco_logging_feature_template":
        broken = r'^    ciphersuite_list_variable\s*= try\(prof.version_ciphersuites_variablevariable, null\)\n'
        source, count = re.subn(broken, '', source, flags=re.M)
        if count != 1:
            raise SystemExit("Logging upstream typo changed")
    public_collection = re.search(r'local.edge_feature_templates\.(\w+)', source).group(1)
    model = (args.provider / "internal/provider" / ("model_" + resource + ".go")).read_text()
    body = model[model.index("toBody(ctx"):model.index("return body")]
    template_type = re.search(r'"templateType", "([^"]+)"', body).group(1)
    schema = json.loads((args.provider / "gen/models/feature_templates" / (template_type + ".json")).read_text())
    if resource in ("sdwan_cisco_thousandeyes_feature_template", "sdwan_security_app_hosting_feature_template"):
        singleton = re.search(r'  virtual_applications = \[\{', source)
        item_source = block(source, singleton.end() - 1)
        loop = re.search(r'for _, item := range data.VirtualApplications \{', body)
        item_body = block(body, loop.end() - 1)
        path = re.search(r'sjson.SetRaw\(body, path\+"([^"]+)\."\+"vipValue\.-1", itemBody\)', item_body).group(1)
        leaf = next(x for x in schema["fields"] if ".".join(x["dataPath"] + [x["key"]]) == path)
        generated = definition(item_source, "each.value", 4, model, item_body, leaf["children"], "item", "itemBody", re.search(r'VirtualApplications\s+\[\](\w+)', model).group(1))
        generated["wrapper"] = {"path": path, "primary_keys": leaf["primaryKeys"]}
    else:
        generated = definition(source, "each.value", 2, model, body, schema["fields"])
    if resource == "sdwan_cisco_vpn_interface_ipsec_feature_template":
        # NaC's published example uses numeric DH groups; Terraform coerces to String.
        field = generated["fields"]["ike.group"]
        field["input_schema"] = {"anyOf": [field["schema"], {"type":"integer", "enum":[int(v) for v in field["schema"]["enum"]]}]}
        field["decimal_string"] = True
    if resource == "sdwan_cisco_logging_feature_template":
        generated["unavailable_inputs"]["tls_profiles[].ciphersuites_variable"] = "Pinned module reads version_ciphersuites_variablevariable instead"
    generated.update(type=template_type, device_types=defaults[public_collection]["device_types"])
    output["templates"][public_collection] = generated
(root / "internal/controlleradapter/sdwan/definitions/classic-templates.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n")
