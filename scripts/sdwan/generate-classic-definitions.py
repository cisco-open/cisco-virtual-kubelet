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
    predicates = {}
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
        predicate = re.fullmatch(r'try\(' + re.escape(alias) + r'\.(\w+), null\) == "([^"\n]+)" \? true : null', expression)
        if predicate:
            inputs[tf] = "$derived:" + tf
            predicates[tf] = {"field":predicate.group(1), "equals":predicate.group(2)}
            continue
        if tf in ("stub_no_summary", "nssa_no_summary"):
            area_type = tf.split("_")[0]
            expected = 'try(a.area_type, null) == "' + area_type + '" && try(a.no_summary, null) == true ? true : try(a.area_type, null) == "' + area_type + '" && try(a.no_summary, null) == null ? false : null'
            if expression != expected:
                raise SystemExit("OSPF area summary condition changed")
            inputs[tf] = "$derived:" + tf
            predicates[tf] = {"field":"area_type", "equals":area_type, "boolean":"no_summary"}
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
        if tf in ("stub", "nssa", "default_information_originate"):
            # These booleans enable a container rather than a vipValue leaf.
            expected_path = "ospf.default-information.originate" if tf == "default_information_originate" else tf
            if not re.search(r'sjson.Set\(' + target + ', ' + prefix + '"' + re.escape(expected_path) + r'", map\[string\]interface\{\}\{\}\)', body):
                raise SystemExit("Unreviewed classic container marker: " + tf)
            mapping[public] = {"path":expected_path, "container":True, "object_type":"node-only" if tf == "default_information_originate" else ""}
            if tf in predicates:
                mapping[public]["predicate"] = predicates[tf]
            continue
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
        if datatype["type"] not in ("string", "boolean", "number", "enum", "ipv4", "ipv4-prefix", "ipv4Range", "mac", "radioButtonList", "passphrase", "restrictedPassphrase", "ike", "ip", "enumList", "datetimelocal") or set(datatype) - {"type", "default", "min", "max", "minLength", "maxLength", "values", "pattern", "label", "range"}:
            raise SystemExit(f"Unreviewed classic value constraints: {tf}: {datatype}")
        value_type = {"String": "string", "Bool": "boolean", "Int64": "integer"}.get(typ)
        if typ in ("Set", "List"):
            value_type = "integer" if datatype["type"] == "number" else "string"
        if value_type is None:
            raise SystemExit("Unsupported classic field type")
        value_schema = {"type": value_type}
        for before, after in [("min", "minimum"), ("max", "maximum"), ("minLength", "minLength"), ("maxLength", "maxLength"), ("pattern", "pattern")]:
            if before in datatype and datatype["type"] != "datetimelocal":
                value_schema[after] = datatype[before]
        if datatype["type"] == "datetimelocal":
            # Manager UI datetime hints are not wire constraints: provider sends
            # the model epoch string verbatim and declares no date validator.
            value_schema["description"] = "Manager datetime epoch string"
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
        original_public = public
        if public in mapping:
            public = "$field:" + tf
        mapping[public] = {"path": path, "schema": value_schema, "set": typ == "Set", "omit_null": omit, "object_type": leaf["objectType"], "boolean_string": "strconv.FormatBool(" + receiver + "." + name + ".ValueBool())" in body}
        if original_public != public:
            mapping[public]["source"] = original_public
        if tf in predicates:
            mapping[public]["predicate"] = predicates[tf]
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
for resource in ["sdwan_cedge_global_feature_template", "sdwan_cli_template_feature_template", "sdwan_cellular_controller_feature_template", "sdwan_cellular_cedge_profile_feature_template", "sdwan_cedge_multicast_feature_template", "sdwan_cisco_bfd_feature_template", "sdwan_cedge_igmp_feature_template", "sdwan_cisco_ntp_feature_template", "sdwan_cisco_omp_feature_template", "sdwan_cedge_pim_feature_template", "sdwan_cisco_thousandeyes_feature_template", "sdwan_security_app_hosting_feature_template", "sdwan_cisco_dhcp_server_feature_template", "sdwan_cisco_vpn_interface_ipsec_feature_template", "sdwan_cisco_snmp_feature_template", "sdwan_cisco_logging_feature_template", "sdwan_cisco_vpn_interface_gre_feature_template", "sdwan_cisco_sig_credentials_feature_template", "sdwan_cisco_security_feature_template", "sdwan_cisco_ospf_feature_template"]:
    source = block(hcl, hcl.index("{", hcl.index('resource "' + resource + '"')))
    ospf_policy_source = None
    if resource == "sdwan_cisco_ospf_feature_template":
        start = source.index("  route_policies =")
        end = source.index("  }]", start) + len("  }]")
        ospf_policy_source = source[start:end]
        source = source[:start] + source[end:]
    if resource == "sdwan_cisco_security_feature_template":
        start = source.index("  authentication_type =")
        end = source.index("  a]", start) + len("  a]")
        expected = '  authentication_type = try(length(each.value.authentication_types) == 0, true) ? null : [for a in each.value.authentication_types :\n    a == "esp" ? "sha1-hmac" :\n    a == "ip-udp-esp" ? "ah-sha1-hmac" :\n    a == "ip-udp-esp-no-id" ? "ah-no-id" :\n  a]'
        if source[start:end] != expected:
            raise SystemExit("Security authentication translation changed")
        source = source[:start] + '  authentication_type = try(each.value.authentication_types, null)' + source[end:]
    gre_acl_source = None
    if resource == "sdwan_cisco_vpn_interface_gre_feature_template":
        start = source.index("  access_lists =")
        end = source.index("  ])", start) + len("  ])")
        gre_acl_source = source[start:end]
        outer = 'access_lists = try(each.value.ipv4_ingress_access_list, each.value.ipv4_ingress_access_list_variable, each.value.ipv4_egress_access_list, each.value.ipv4_egress_access_list_variable, null) == null ? null : flatten(['
        if not gre_acl_source.strip().startswith(outer):
            raise SystemExit("GRE ACL outer condition changed")
        for direction in ("ingress", "egress"):
            condition = 'try(each.value.ipv4_' + direction + '_access_list, each.value.ipv4_' + direction + '_access_list_variable, null) == null ? [] : [{'
            if gre_acl_source.count(condition) != 1:
                raise SystemExit("GRE ACL row condition changed")
        source = source[:start] + source[end:]
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
    if ospf_policy_source:
        expected = 'route_policies = try(each.value.route_policy, each.value.route_policy_variable, null) == null ? null : [{'
        if not ospf_policy_source.strip().startswith(expected):
            raise SystemExit("OSPF policy condition changed")
        item_source = block(ospf_policy_source, ospf_policy_source.index("{"))
        loop = re.search(r'for _, item := range data.RoutePolicies \{', body)
        item_body = block(body, loop.end()-1)
        path = re.search(r'sjson.SetRaw\(body, path\+"([^" ]+)\."\+"vipValue\.-1", itemBody\)', item_body).group(1)
        leaf = next(x for x in schema["fields"] if ".".join(x["dataPath"]+[x["key"]]) == path)
        child_struct = re.search(r'RoutePolicies\s+\[\](\w+)',model).group(1)
        child = definition(item_source,"each.value",4,model,item_body,leaf["children"],"item","itemBody",child_struct)
        generated["conditional_trees"] = [{"path":path,"primary_keys":leaf["primaryKeys"],"definition":child,"triggers":["route_policy","route_policy_variable"]}]
    if resource == "sdwan_cisco_security_feature_template":
        generated["fields"]["$field:authentication_type"]["array_values"] = {"esp":"sha1-hmac", "ip-udp-esp":"ah-sha1-hmac", "ip-udp-esp-no-id":"ah-no-id"}
    if resource == "sdwan_cisco_sig_credentials_feature_template":
        if 'sjson.Set(body, "isGlobal", true)' not in body:
            raise SystemExit("SIG global template flag changed")
        generated["global"] = True
        for public, field in generated["fields"].items():
            if public in ("umbrella_api_key", "umbrella_api_secret"):
                field["variable_value"] = ""
            elif public.startswith("zscaler_"):
                field["variable_value_from_input"] = True
                field["constant_variable_name"] = True
        generated["description_by_name"] = {"Cisco-Umbrella-Global-Credentials":"Global credentials for umbrella"}
        generated["description_default"] = "Global credentials for zscaler"
    if gre_acl_source:
        loop = re.search(r'for _, item := range data.AccessLists \{', body)
        item_body = block(body, loop.end() - 1)
        path = re.search(r'sjson.SetRaw\(body, path\+"([^"]+)\."\+"vipValue\.-1", itemBody\)', item_body).group(1)
        leaf = next(x for x in schema["fields"] if ".".join(x["dataPath"] + [x["key"]]) == path)
        child_struct = re.search(r'AccessLists\s+\[\](\w+)', model).group(1)
        children = list(re.finditer(r'\[\] : \[\{', gre_acl_source))
        if len(children) != 2:
            raise SystemExit("GRE ACL conditions changed")
        generated["conditional_trees"] = []
        for match, direction in zip(children, ["ingress", "egress"]):
            item_source = block(gre_acl_source, match.end() - 1)
            child = definition(item_source, "each.value", 6, model, item_body, leaf["children"], "item", "itemBody", child_struct)
            generated["conditional_trees"].append({"path":path, "primary_keys":leaf["primaryKeys"], "definition":child, "triggers":["ipv4_"+direction+"_access_list", "ipv4_"+direction+"_access_list_variable"]})
    if resource == "sdwan_cisco_vpn_interface_ipsec_feature_template":
        # NaC's published example uses numeric DH groups; Terraform coerces to String.
        field = generated["fields"]["ike.group"]
        field["input_schema"] = {"anyOf": [field["schema"], {"type":"integer", "enum":[int(v) for v in field["schema"]["enum"]]}]}
        field["decimal_string"] = True
    if resource == "sdwan_cisco_security_feature_template":
        field = generated["fields"]["replay_window"]
        field["input_schema"] = {"anyOf":[field["schema"], {"type":"integer", "enum":[int(v) for v in field["schema"]["enum"]]}]}
        field["decimal_string"] = True
        key_fields = generated["trees"]["keys"]["definition"]["fields"]
        # Manager populates these dropdowns from keychains; an empty UI enum
        # does not mean no wire values are valid.
        for public in ("id", "key_chain_name"):
            if key_fields[public]["schema"] != {"type":"string", "enum":[]}:
                raise SystemExit("Security dynamic key dropdown changed")
            key_fields[public]["schema"] = {"type":"string"}
        key_fields["id"]["input_schema"] = {"type":"integer", "minimum":0, "maximum":2147483647}
        key_fields["id"]["decimal_string"] = True
        for public, field in generated["trees"]["keys"]["definition"]["fields"].items():
            if public.endswith("_epoch"):
                field["input_schema"] = {"anyOf":[{"type":"string"}, {"type":"integer"}]}
                field["decimal_string"] = True
    if resource == "sdwan_cisco_ospf_feature_template":
        generated["unavailable_inputs"]["areas[].interfaces[].dead_interval_variable"] = "Pinned module reads dead_intervale_variable instead"
    if resource == "sdwan_cisco_logging_feature_template":
        generated["unavailable_inputs"]["tls_profiles[].ciphersuites_variable"] = "Pinned module reads version_ciphersuites_variablevariable instead"
    generated.update(type=template_type, device_types=defaults[public_collection]["device_types"])
    output["templates"][public_collection] = generated
(root / "internal/controlleradapter/sdwan/definitions/classic-templates.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n")
