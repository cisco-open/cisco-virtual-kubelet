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
    inputs, required, trees, optional, conditions, unavailable, literals, ranges, wrapped, boolean_choices, present, value_maps, fallbacks, joins, uppers, translations = {}, [], {}, "", {}, {}, {}, {}, set(), {}, {}, {}, {}, {}, set(), {}
    predicates = {}
    conditional_trees = []
    for tf, expression in re.findall(r'^' + ' ' * indent + r'(\w+)\s*=\s*(.*)$', source, re.M):
        if indent == 2 and tf in {"for_each", "name", "description", "device_types", "depends_on"}:
            continue
        if re.fullmatch(r'"[^"\n]*"|[0-9]+|true|false', expression):
            inputs[tf] = "$literal:" + tf
            literals[tf] = json.loads(expression)
            continue
        joined = re.fullmatch(r'join\(",", (?:try\()?'+re.escape(alias)+r'\.([\w.]+)(?:, local\.defaults\.sdwan\.edge_feature_templates\.[\w.]+\))?\)', expression)
        if joined:
            inputs[tf] = joined.group(1)
            joins[tf] = True
            continue
        upper = re.fullmatch(r'try\(upper\(' + re.escape(alias) + r'\.([\w.]+)\), null\)', expression)
        if upper:
            inputs[tf] = upper.group(1)
            uppers.add(tf)
            continue
        translated = re.fullmatch(r'try\(' + re.escape(alias) + r'\.([\w.]+) == "([^"]+)" \? "([^"]+)" : ' + re.escape(alias) + r'\.\1, null\)', expression)
        if translated:
            inputs[tf] = translated.group(1)
            translations[tf] = {translated.group(2):translated.group(3)}
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
        if expression == alias or expression == 'try(' + alias + ', null)':
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
        coalesce = re.fullmatch(r'try\(' + re.escape(alias) + r'\.([\w.]+), ' + re.escape(alias) + r'\.([\w.]+), null\)', expression)
        if coalesce:
            inputs[tf] = coalesce.group(1)
            fallbacks[tf] = coalesce.group(2)
            continue
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
        if expression == "[{":
            item_source = block(source, source.index("{", source.index(expression)))
            go_name, child_struct = re.search(r'(\w+)\s+\[\](\w+)\s+`tfsdk:"' + tf + '"`', struct_body).groups()
            loop = re.search(r'for _, (\w+) := range ' + receiver + r'\.' + go_name + r' \{', body)
            item_body = block(body, loop.end()-1)
            item_receiver = loop.group(1)
            item_target = re.search(r'(\w+) := ""', item_body).group(1)
            path = re.search(r'sjson.SetRaw\(' + target + ', ' + prefix + r'"([^" ]+)\."\+"vipValue\.-1", ' + item_target + r'\)', item_body).group(1)
            leaf = leaves[path]
            child = definition(item_source, alias, indent+2, model, item_body, leaf["children"], item_receiver, item_target, child_struct)
            conditional_trees.append({"path":path,"primary_keys":leaf["primaryKeys"],"definition":child,"triggers":[]})
            continue
        if tf == "route_policies" and alias == "af" and expression.endswith(' == null ? null : flatten(['):
            start = source.index(expression)
            end = source.index("      ])", start) + len("      ])")
            branch = source[start:end]
            go_name, child_struct = re.search(r'(\w+)\s+\[\](\w+)\s+`tfsdk:"route_policies"`', struct_body).groups()
            loop = re.search(r'for _, (\w+) := range ' + receiver + r'\.' + go_name + r' \{', body)
            item_body = block(body, loop.end()-1)
            item_receiver = loop.group(1)
            item_target = re.search(r'(\w+) := ""', item_body).group(1)
            path = re.search(r'sjson.SetRaw\(' + target + ', ' + prefix + r'"([^" ]+)\."\+"vipValue\.-1", ' + item_target + r'\)', item_body).group(1)
            leaf = leaves[path]
            children = list(re.finditer(r'\[\] : \[\{', branch))
            if len(children) != 2:
                raise SystemExit("BGP neighbor route policy conditions changed")
            for match, direction in zip(children, ("in", "out")):
                item_source = block(branch, match.end()-1)
                child = definition(item_source, alias, indent+4, model, item_body, leaf["children"], item_receiver, item_target, child_struct)
                conditional_trees.append({"path":path,"primary_keys":leaf["primaryKeys"],"definition":child,"triggers":["route_policy_"+direction,"route_policy_"+direction+"_variable"]})
            continue
        index = re.fullmatch(r'index\(each\.value\.([\w.]+), ' + re.escape(alias) + r'\)', expression)
        if index and tf == "name":
            inputs[tf] = "$index"
            continue
        conditional = re.fullmatch(r'try\((.+), null\) == null \? null : \[\{', expression)
        if conditional:
            triggers = [part.removeprefix(alias + ".") for part in conditional.group(1).split(", ")]
            if not triggers or any("." in trigger for trigger in triggers):
                raise SystemExit("Unreviewed classic conditional trigger: " + tf)
            item_source = block(source, source.index("{", source.index(expression)))
            go_name, child_struct = re.search(r'(\w+)\s+\[\](\w+)\s+`tfsdk:"' + tf + '"`', struct_body).groups()
            loop = re.search(r'for _, (\w+) := range ' + receiver + r'\.' + go_name + r' \{', body)
            item_body = block(body, loop.end()-1)
            item_receiver = loop.group(1)
            item_target = re.search(r'(\w+) := ""', item_body).group(1)
            path = re.search(r'sjson.SetRaw\(' + target + ', ' + prefix + r'"([^" ]+)\."\+"vipValue\.-1", ' + item_target + r'\)', item_body).group(1)
            leaf = leaves[path]
            child = definition(item_source, alias, indent+2, model, item_body, leaf["children"], item_receiver, item_target, child_struct)
            conditional_trees.append({"path":path,"primary_keys":leaf["primaryKeys"],"definition":child,"triggers":triggers})
            continue
        array = re.search(r'\[for (\w+) in (?:try\()?' + re.escape(alias) + r'\.([\w.]+)(?:, \[\]\))? : \{', expression)
        if not array:
            raise SystemExit(f"Unreviewed classic expression: {tf} ({alias}: {expression})")
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
        if tf in ("stub", "nssa", "default_information_originate", "nat", "nat64_interface", "nat66_interface") and typ == "Bool" and tf not in literals and (tf != "nat" or target == "body") and not (tf == "default_information_originate" and target != "body"):
            # These booleans enable a container rather than a vipValue leaf.
            expected_path = {"default_information_originate":"ospf.default-information.originate", "nat64_interface":"nat64", "nat66_interface":"nat66"}.get(tf, tf)
            if not re.search(r'sjson.Set\(' + target + ', ' + prefix + '"' + re.escape(expected_path) + r'", map\[string\]interface\{\}\{\}\)', body):
                raise SystemExit(f"Unreviewed classic container marker: {tf} in {receiver}/{target}, alias={alias}, expression={expression}, public={public}, literal={literals.get(tf)}")
            mapping[public] = {"path":expected_path, "container":True, "object_type":"node-only" if tf in ("default_information_originate", "nat") else ""}
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
        datatype = dict(leaf["dataType"])
        if tf in ("zscaler_surrogate_idle_time", "zscaler_surrogate_refresh_time") and datatype.get("max:") == 4294967296:
            datatype["max"] = datatype.pop("max:")
        if datatype["type"] not in ("string", "boolean", "number", "float", "enum", "ipv4", "ipv6", "ipv4-prefix", "ipv6-prefix", "ipv4Range", "mac", "radioButtonList", "passphrase", "restrictedPassphrase", "ike", "ip", "enumList", "datetimelocal", "interfaceList", "xConnect", "tlocExtension", "numberFixedInterval", "stringList", "holdTime", "remoteAS") or set(datatype) - {"type", "default", "min", "max", "minLength", "maxLength", "values", "pattern", "regex", "label", "range", "interval"}:
            raise SystemExit(f"Unreviewed classic value constraints: {tf}: {datatype}")
        value_type = {"String": "string", "Bool": "boolean", "Int64": "integer", "Float64": "number"}.get(typ)
        if typ in ("Set", "List"):
            value_type = "integer" if datatype["type"] == "number" else "string"
        if value_type is None:
            raise SystemExit("Unsupported classic field type")
        value_schema = {"type": value_type}
        for before, after in [("min", "minimum"), ("max", "maximum"), ("minLength", "minLength"), ("maxLength", "maxLength"), ("pattern", "pattern")]:
            if before in datatype and datatype["type"] != "datetimelocal":
                value_schema[after] = datatype[before]
        if datatype["type"] == "numberFixedInterval":
            value_schema["multipleOf"] = datatype["interval"]
        if datatype["type"] == "datetimelocal":
            # Manager UI datetime hints are not wire constraints: provider sends
            # the model epoch string verbatim and declares no date validator.
            value_schema["description"] = "Manager datetime epoch string"
        if "regex" in datatype:
            value_schema["pattern"] = datatype["regex"]
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
        if datatype["type"] in ("ipv4", "ipv6"):
            value_schema["format"] = datatype["type"]
        if datatype["type"] in ("ipv4Range", "mac"):
            value_schema["format"] = datatype["type"]
        if datatype["type"] in ("ipv4-prefix", "ipv6-prefix"):
            value_schema["format"] = datatype["type"]
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
        if tf in translations:
            mapping[public]["values"] = translations[tf]
        if tf in boolean_choices:
            mapping[public].update(boolean_choice=boolean_choices[tf], input_default=defaults["snmp_templates"]["communities"]["authorization_read_only"])
        if tf in wrapped:
            mapping[public]["wrap_list"] = True
        if tf in ranges:
            mapping[public]["ranges"] = ranges[tf]
        if tf in fallbacks:
            mapping[public]["fallback"] = fallbacks[tf]
        if tf in joins:
            mapping[public]["join_comma"] = True
            mapping[public]["input_schema"] = {"type":"array", "items":{"type":"string"}}
            if datatype["type"] == "stringList" and datatype.get("max"):
                mapping[public]["input_schema"]["maxItems"] = datatype["max"]
        if tf in uppers:
            mapping[public]["upper"] = True
        if tf in literals:
            value = literals[tf]
            if typ == "String":
                value = str(value)
            elif typ == "Bool" and value in ("true", "false"):
                value = value == "true"
            mapping[public].update(literal=value)
        if tf in conditions:
            mapping[public].update(derived=True, when=conditions[tf][0], derived_value=conditions[tf][1])
        if tf + "_variable" in inputs:
            mapping[public]["variable"] = inputs[tf + "_variable"]
    for tf, public in inputs.items():
        if not tf.endswith("_variable") or tf.removesuffix("_variable") in inputs:
            continue
        name, typ = fields[tf]
        if typ != "String":
            raise SystemExit("Unsupported standalone classic variable: " + tf)
        paths = set(re.findall(prefix + r'"([^"]+)\."\+"vipVariableName", ' + receiver + r'\.' + name + r'\.ValueString\(\)', body))
        if len(paths) != 1:
            raise SystemExit("Ambiguous standalone classic variable: " + tf)
        path = paths.pop()
        leaf = leaves[path]
        mapping[public] = {"path":path,"schema":{"type":"string"},"object_type":leaf["objectType"],"variable":public,"omit_null":True}
    result = {"fields": mapping, "required": required, "defaults": baseline, "trees": trees, "optional": optional, "unavailable_inputs": unavailable}
    if conditional_trees:
        result["conditional_trees"] = conditional_trees
    return result

hcl = (args.module / "sdwan_feature_templates.tf").read_text()
defaults = yaml.safe_load((args.module / "defaults/defaults.yaml").read_text())["defaults"]["sdwan"]["edge_feature_templates"]
output = {"license": "MPL-2.0", "module_commit": lock["module"]["commit"], "provider_commit": lock["provider"]["commit"], "templates": {}}
for resource in ["sdwan_cedge_global_feature_template", "sdwan_cli_template_feature_template", "sdwan_cellular_controller_feature_template", "sdwan_cellular_cedge_profile_feature_template", "sdwan_cedge_multicast_feature_template", "sdwan_cisco_bfd_feature_template", "sdwan_cedge_igmp_feature_template", "sdwan_cisco_ntp_feature_template", "sdwan_cisco_omp_feature_template", "sdwan_cedge_pim_feature_template", "sdwan_cisco_thousandeyes_feature_template", "sdwan_security_app_hosting_feature_template", "sdwan_cisco_dhcp_server_feature_template", "sdwan_cisco_vpn_interface_ipsec_feature_template", "sdwan_cisco_snmp_feature_template", "sdwan_cisco_logging_feature_template", "sdwan_cisco_vpn_interface_gre_feature_template", "sdwan_cisco_sig_credentials_feature_template", "sdwan_cisco_security_feature_template", "sdwan_cisco_ospf_feature_template", "sdwan_vpn_interface_svi_feature_template", "sdwan_vpn_interface_cellular_feature_template", "sdwan_switchport_feature_template", "sdwan_cisco_system_feature_template", "sdwan_cisco_vpn_feature_template", "sdwan_cisco_vpn_interface_feature_template", "sdwan_cedge_aaa_feature_template", "sdwan_cisco_secure_internet_gateway_feature_template", "sdwan_cisco_bgp_feature_template"]:
    source = block(hcl, hcl.index("{", hcl.index('resource "' + resource + '"')))
    ospf_policy_source = None
    if resource == "sdwan_cisco_ospf_feature_template":
        start = source.index("  route_policies =")
        end = source.index("  }]", start) + len("  }]")
        ospf_policy_source = source[start:end]
        source = source[:start] + source[end:]
    bgp_address_families_source = None
    if resource == "sdwan_cisco_bgp_feature_template":
        start = source.index("  address_families = flatten([")
        end = source.index("  ])", start) + len("  ])")
        bgp_address_families_source = source[start:end]
        source = source[:start] + source[end:]
        missing = r'(?m)^\s*optional_variable\s*= try\((n|af)\.optional_variable, null\)\n'
        source, count = re.subn(missing, '', source)
        if count != 4:
            raise SystemExit("BGP provider-missing optional variable fields changed")
    if resource == "sdwan_switchport_feature_template":
        expected = 'switchport_trunk_allowed_vlans                    = length(concat(try(interface.trunk_allowed_vlans, []), try(interface.trunk_allowed_vlans_ranges, []))) > 0 ? join(",", concat([for p in try(interface.trunk_allowed_vlans, []) : p], [for r in try(interface.trunk_allowed_vlans_ranges, []) : "${r.from}-${r.to}"])) : null'
        if expected not in source:
            raise SystemExit("Switchport VLAN range mapping changed")
        source = source.replace(expected, 'switchport_trunk_allowed_vlans = try(interface.trunk_allowed_vlans, null)')
        mac_expr = 'mac_address          = try(sma.mac_address, null) == null ? null : format("%s.%s.%s", substr(replace(sma.mac_address, ":", ""), 0, 4), substr(replace(sma.mac_address, ":", ""), 4, 4), substr(replace(sma.mac_address, ":", ""), 8, 4))'
        if mac_expr not in source: raise SystemExit("Switchport MAC mapping changed")
        source = source.replace(mac_expr, 'mac_address = try(sma.mac_address, null)')
    if resource == "sdwan_cisco_security_feature_template":
        start = source.index("  authentication_type =")
        end = source.index("  a]", start) + len("  a]")
        expected = '  authentication_type = try(length(each.value.authentication_types) == 0, true) ? null : [for a in each.value.authentication_types :\n    a == "esp" ? "sha1-hmac" :\n    a == "ip-udp-esp" ? "ah-sha1-hmac" :\n    a == "ip-udp-esp-no-id" ? "ah-no-id" :\n  a]'
        if source[start:end] != expected:
            raise SystemExit("Security authentication translation changed")
        source = source[:start] + '  authentication_type = try(each.value.authentication_types, null)' + source[end:]
    svi_acl_sources = []
    if resource in ("sdwan_vpn_interface_svi_feature_template", "sdwan_vpn_interface_cellular_feature_template"):
        for family in ("ipv4", "ipv6"):
            start = source.index("  " + family + "_access_lists =")
            end = source.index("  ])", start) + len("  ])")
            acl_source = source[start:end]
            if not acl_source.strip().startswith(f'{family}_access_lists = try('):
                raise SystemExit("SVI ACL condition changed")
            svi_acl_sources.append((family, acl_source))
            source = source[:start] + source[end:]
    cellular_encapsulation_source = None
    if resource in ("sdwan_vpn_interface_cellular_feature_template", "sdwan_cisco_vpn_interface_feature_template"):
        start = source.index("  tunnel_interface_encapsulations =")
        end = source.index("  ])", start) + len("  ])")
        cellular_encapsulation_source = source[start:end]
        expected = 'tunnel_interface_encapsulations = try(each.value.tunnel_interface.gre_encapsulation, each.value.tunnel_interface.ipsec_encapsulation, null) == null ? null : flatten(['
        if not cellular_encapsulation_source.strip().startswith(expected):
            raise SystemExit("Tunnel encapsulation condition changed")
        source = source[:start] + source[end:]
    cellular_policer_source = None
    if resource == "sdwan_vpn_interface_cellular_feature_template":
        start = source.index("  policers =")
        end = source.index("  ])", start) + len("  ])")
        cellular_policer_source = source[start:end]
        source = source[:start] + source[end:]
    gre_acl_sources = []
    if resource in ("sdwan_cisco_vpn_interface_gre_feature_template", "sdwan_cisco_vpn_interface_feature_template"):
        families = ("ipv4",) if resource.endswith("_gre_feature_template") else ("ipv4", "ipv6")
        for family in families:
            field = "access_lists" if family == "ipv4" else "ipv6_access_lists"
            start = source.index("  " + field + " =")
            end = source.index("  ])", start) + len("  ])")
            acl_source = source[start:end]
            outer = field + ' = try(each.value.' + family + '_ingress_access_list, each.value.' + family + '_ingress_access_list_variable, each.value.' + family + '_egress_access_list, each.value.' + family + '_egress_access_list_variable, null) == null ? null : flatten(['
            if not acl_source.strip().startswith(outer):
                raise SystemExit("VPN interface ACL outer condition changed")
            for direction in ("ingress", "egress"):
                condition = 'try(each.value.' + family + '_' + direction + '_access_list, each.value.' + family + '_' + direction + '_access_list_variable, null) == null ? [] : [{'
                if acl_source.count(condition) != 1:
                    raise SystemExit("VPN interface ACL row condition changed")
            gre_acl_sources.append((family, field, acl_source))
            source = source[:start] + source[end:]
    if resource == "sdwan_cisco_logging_feature_template":
        broken = r'^    ciphersuite_list_variable\s*= try\(prof.version_ciphersuites_variablevariable, null\)\n'
        source, count = re.subn(broken, '', source, flags=re.M)
        if count != 1:
            raise SystemExit("Logging upstream typo changed")
    if resource == "sdwan_cisco_system_feature_template":
        # This expression captures obj from the parent loop while the row itself
        # iterates scalar i values; the generic nested mapping cannot represent it.
        unsupported = '      track_id_variable = try(obj.group_trackers_variable, null)\n'
        if source.count(unsupported) != 1:
            raise SystemExit("System object tracker variable mapping changed")
        source = source.replace(unsupported, '')
        for field in ("interval", "multiplier", "threshold"):
            original = rf'(?m)^(\s*{field})\s+= try\(obj\.{field}, can\(obj\.{field}_variable\) \? null : local\.defaults\.sdwan\.edge_feature_templates\.system_templates\.endpoint_trackers\.{field}\)$'
            source, count = re.subn(original, rf'\1 = try(obj.{field}, null)', source)
            if count != 1:
                raise SystemExit("System tracker default expression changed: " + field)
        tracker_type = 'try(can(obj.group_trackers) ? "tracker-group" : try(obj.type, can(obj.type_variable) ? null : local.defaults.sdwan.edge_feature_templates.system_templates.endpoint_trackers.type))'
        if source.count(tracker_type) != 1:
            raise SystemExit("System tracker type expression changed")
        source = source.replace(tracker_type, 'try(obj.type, null)')
        for field in ("transport_endpoint_ip", "transport_endpoint_port", "transport_endpoint_protocol"):
            pattern = rf'(?m)^\s*{field}(?:_variable)?\s*= try\(obj\.{field}(?:_variable)?, null\)\n'
            source, count = re.subn(pattern, '', source)
            if count != 2:
                raise SystemExit("System transport endpoint fields changed: " + field)
        for field in ("enhanced_app_aware_routing", "enhanced_app_aware_routing_variable"):
            pattern = rf'(?m)^\s*{field}\s*= try\(each\.value\.{field}, null\)\n'
            source, count = re.subn(pattern, '', source)
            if count != 1:
                raise SystemExit("System EPFR field changed: " + field)
    if resource == "sdwan_cisco_vpn_feature_template":
        for field in ("dns_hosts", "dns_ipv4_servers", "dns_ipv6_servers"):
            start = source.index("  " + field + " =")
            end = source.index("  ])", start) + len("  ])")
            source = source[:start] + source[end:]
        dia = 'try(route.next_hop_dia, null) == true ? 0 : null'
        if source.count(dia) != 1:
            raise SystemExit("VPN direct-internet-access mapping changed")
        source = source.replace(dia, 'try(route.next_hop_dia, null)')
        subtype = 'protocol_sub_type     = try(route.protocol, "") == "ospf" ? ["external"] : null\n'
        if source.count(subtype) != 1:
            raise SystemExit("VPN OMP route subtype mapping changed")
        source = source.replace(subtype, '')
        for name in ("exp", "imp"):
            derived = f'protocol_sub_type = try({name}.protocol, null) != null ? ["external"] : null\n'
            if source.count(derived) != 1:
                raise SystemExit("VPN route leak subtype mapping changed")
            source = source.replace(derived, '')
    if resource == "sdwan_cisco_vpn_interface_feature_template":
        # The published model expresses DHCP as a boolean selector; keep the
        # input name while the compiler handles the provider's VIP boolean.
        dhcp = 'dhcp                                           = try(each.value.ipv4_address_dhcp, null) == true ? true : null'
        if source.count(dhcp) != 1:
            raise SystemExit("VPN interface DHCP selector changed")
        source = source.replace(dhcp, 'dhcp = try(each.value.ipv4_address_dhcp, null)')
    if resource == "sdwan_cisco_secure_internet_gateway_feature_template":
        # Conditional SIG interface inputs are normalized from the public
        # provider/tunnel selectors before the shared classic compiler runs.
        conditional_fields = ("auto_tunnel_mode", "ike_ciphersuite", "ike_ciphersuite_variable", "ike_group", "ike_group_variable", "ike_pre_shared_key", "ike_pre_shared_key_variable", "ike_pre_shared_key_dynamic", "ike_pre_shared_key_local_id", "ike_pre_shared_key_local_id_variable", "ike_pre_shared_key_remote_id", "ike_pre_shared_key_remote_id_variable", "ike_rekey_interval", "ike_rekey_interval_variable", "ike_version", "ipsec_ciphersuite", "ipsec_ciphersuite_variable", "ipsec_perfect_forward_secrecy", "ipsec_perfect_forward_secrecy_variable", "ipsec_rekey_interval", "ipsec_rekey_interval_variable", "ipsec_replay_window", "ipsec_replay_window_variable", "sig_provider", "tunnel_destination", "tunnel_destination_variable", "tunnel_public_ip", "tunnel_public_ip_variable")
        for field in conditional_fields:
            pattern = r'(?m)^(    ' + field + r')\s*= ([^\n]+)$'
            matches = list(re.finditer(pattern, source))
            if len(matches) != 1:
                raise SystemExit("SIG conditional field changed: " + field)
            public = {"tunnel_public_ip":"tunnel_public_source_ip", "tunnel_public_ip_variable":"tunnel_public_source_ip_variable"}.get(field, field)
            source = re.sub(pattern, lambda match: match.group(1) + ' = try(interface.' + public + ', null)', source)
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
    if bgp_address_families_source:
        loop = re.search(r'for _, item := range data.AddressFamilies \{', body)
        item_body = block(body, loop.end()-1)
        path = re.search(r'sjson.SetRaw\(body, path\+"([^" ]+)\."\+"vipValue\.-1", itemBody\)', item_body).group(1)
        leaf = next(x for x in schema["fields"] if ".".join(x["dataPath"]+[x["key"]]) == path)
        child_struct = re.search(r'AddressFamilies\s+\[\](\w+)', model).group(1)
        children = list(re.finditer(r'== null \? \[\] : \[\{', bgp_address_families_source))
        if len(children) != 2:
            raise SystemExit("BGP address family conditions changed")
        for match, family in zip(children, ("ipv4", "ipv6")):
            item_source = block(bgp_address_families_source, match.end()-1)
            child = definition(item_source, "each.value", 6, model, item_body, leaf["children"], "item", "itemBody", child_struct)
            generated.setdefault("conditional_trees",[]).append({"path":path,"primary_keys":leaf["primaryKeys"],"definition":child,"triggers":[family+"_address_family"]})
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
    for family, collection, gre_acl_source in gre_acl_sources:
        go_collection = "AccessLists" if family == "ipv4" else "Ipv6AccessLists"
        loop = re.search(r'for _, item := range data.' + go_collection + r' \{', body)
        item_body = block(body, loop.end() - 1)
        path = re.search(r'sjson.SetRaw\(body, path\+"([^"]+)\."\+"vipValue\.-1", itemBody\)', item_body).group(1)
        leaf = next(x for x in schema["fields"] if ".".join(x["dataPath"] + [x["key"]]) == path)
        child_struct = re.search(go_collection+r'\s+\[\](\w+)', model).group(1)
        children = list(re.finditer(r'\[\] : \[\{', gre_acl_source))
        if len(children) != 2:
            raise SystemExit("GRE ACL conditions changed")
        generated.setdefault("conditional_trees", [])
        for match, direction in zip(children, ["ingress", "egress"]):
            item_source = block(gre_acl_source, match.end() - 1)
            child = definition(item_source, "each.value", 6, model, item_body, leaf["children"], "item", "itemBody", child_struct)
            generated["conditional_trees"].append({"path":path, "primary_keys":leaf["primaryKeys"], "definition":child, "triggers":[family+"_"+direction+"_access_list", family+"_"+direction+"_access_list_variable"]})
    if cellular_encapsulation_source:
        loop = re.search(r'for _, item := range data.TunnelInterfaceEncapsulations \{', body)
        item_body = block(body, loop.end()-1)
        path = re.search(r'sjson.SetRaw\(body, path\+"([^" ]+)\."\+"vipValue\.-1", itemBody\)', item_body).group(1)
        leaf = next(x for x in schema["fields"] if ".".join(x["dataPath"]+[x["key"]]) == path)
        child_struct = re.search(r'TunnelInterfaceEncapsulations\s+\[\](\w+)', model).group(1)
        children = list(re.finditer(r'\[\] : \[\{', cellular_encapsulation_source))
        if len(children) != 2:
            raise SystemExit("Cellular encapsulation rows changed")
        for match, kind in zip(children, ("gre", "ipsec")):
            item_source = block(cellular_encapsulation_source, match.end()-1)
            child = definition(item_source, "each.value", 6, model, item_body, leaf["children"], "item", "itemBody", child_struct)
            generated.setdefault("conditional_trees",[]).append({"path":path,"primary_keys":leaf["primaryKeys"],"definition":child,"triggers":["tunnel_interface."+kind+"_encapsulation"]})
    if cellular_policer_source:
        loop = re.search(r'for _, item := range data.Policers \{', body)
        item_body = block(body, loop.end()-1)
        path = re.search(r'sjson.SetRaw\(body, path\+"([^" ]+)\."\+"vipValue\.-1", itemBody\)', item_body).group(1)
        leaf = next(x for x in schema["fields"] if ".".join(x["dataPath"]+[x["key"]]) == path)
        child_struct = re.search(r'Policers\s+\[\](\w+)', model).group(1)
        children = list(re.finditer(r'\[\] : \[\{', cellular_policer_source))
        if len(children) != 2: raise SystemExit("Cellular policer rows changed")
        for match, direction in zip(children, ("ingress", "egress")):
            item_source = block(cellular_policer_source, match.end()-1)
            child = definition(item_source, "each.value", 6, model, item_body, leaf["children"], "item", "itemBody", child_struct)
            generated.setdefault("conditional_trees",[]).append({"path":path,"primary_keys":leaf["primaryKeys"],"definition":child,"triggers":[direction+"_policer_name",direction+"_policer_name_variable"]})
        generated.setdefault("unavailable_inputs",{})["ingress_policer_name_variable"] = "Pinned module only passes a name to the provider's policer row"
        generated["unavailable_inputs"]["egress_policer_name_variable"] = "Pinned module only passes a name to the provider's policer row"
    for family, acl_source in svi_acl_sources:
        loop = re.search(r'for _, item := range data.' + family.title() + r'AccessLists \{', body)
        item_body = block(body, loop.end()-1)
        path = re.search(r'sjson.SetRaw\(body, path\+"([^" ]+)\."\+"vipValue\.-1", itemBody\)', item_body).group(1)
        leaf = next(x for x in schema["fields"] if ".".join(x["dataPath"]+[x["key"]]) == path)
        child_struct = re.search(family.title()+r'AccessLists\s+\[\](\w+)', model).group(1)
        children = list(re.finditer(r'\[\] : \[\{', acl_source))
        if len(children) != 2:
            raise SystemExit("SVI ACL rows changed")
        for match, direction in zip(children, ("ingress", "egress")):
            item_source = block(acl_source, match.end()-1)
            child = definition(item_source, "each.value", 6, model, item_body, leaf["children"], "item", "itemBody", child_struct)
            generated.setdefault("conditional_trees",[]).append({"path":path,"primary_keys":leaf["primaryKeys"],"definition":child,"triggers":[family+"_"+direction+"_access_list",family+"_"+direction+"_access_list_variable"]})
    if resource == "sdwan_cisco_vpn_interface_ipsec_feature_template":
        # NaC's published example uses numeric DH groups; Terraform coerces to String.
        field = generated["fields"]["ike.group"]
        field["input_schema"] = {"anyOf": [field["schema"], {"type":"integer", "enum":[int(v) for v in field["schema"]["enum"]]}]}
        field["decimal_string"] = True
    if resource == "sdwan_switchport_feature_template":
        field = generated["trees"]["interfaces"]["definition"]["fields"]["trunk_allowed_vlans"]
        field["joined_ranges"] = "trunk_allowed_vlans_ranges"
        mac = generated["trees"]["static_mac_addresses"]["definition"]["fields"]["mac_address"]
        mac["mac_format"] = "cisco-dot"
        mac["input_schema"] = {"type":"string","format":"mac"}
        field["input_schema"] = {"type":"array","items":{"type":"integer","minimum":1,"maximum":4094}}
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
    if resource == "sdwan_cisco_system_feature_template":
        fields = generated["trees"]["endpoint_trackers"]["definition"]["fields"]
        for field in ("interval", "multiplier", "threshold", "type"):
            fields[field]["input_default"] = defaults["system_templates"]["endpoint_trackers"][field]
        if fields["type"]["schema"] != {"type":"string","enum":["interface","static-route"]}:
            raise SystemExit("System tracker type schema changed")
        fields["type"]["schema"]["enum"].append("tracker-group")
        generated["unavailable_inputs"]["object_trackers[].group_trackers_variable"] = "Pinned module captures the parent variable inside a scalar child row"
        for field in ("transport_endpoint_ip", "transport_endpoint_port", "transport_endpoint_protocol"):
            generated["unavailable_inputs"][f"endpoint_trackers[].{field}"] = "Pinned provider model omits this field"
            generated["unavailable_inputs"][f"endpoint_trackers[].{field}_variable"] = "Pinned provider model omits this field"
        for field in ("enhanced_app_aware_routing", "enhanced_app_aware_routing_variable"):
            generated["unavailable_inputs"][field] = "Pinned Manager feature-template schema omits EPFR"
    if resource == "sdwan_cisco_vpn_feature_template":
        field = generated["trees"]["ipv4_static_routes"]["definition"]["fields"]["next_hop_dia"]
        field["boolean_zero"] = True
        field["input_schema"] = {"type":"boolean"}
        for name in ("ipv6_primary_dns_server_variable", "ipv6_secondary_dns_server_variable"):
            generated["unavailable_inputs"][name] = "Pinned provider omits IPv6 DNS server address variables"
        generated["unavailable_inputs"]["omp_advertise_ipv4_routes[].protocol_sub_type"] = "Pinned module derives external subtype for OSPF"
        generated["unavailable_inputs"]["route_global_exports[].protocol_sub_type"] = "Pinned module derives external subtype"
        generated["unavailable_inputs"]["route_global_imports[].protocol_sub_type"] = "Pinned module derives external subtype"
    if resource == "sdwan_cedge_aaa_feature_template":
        generated["fields"]["authentication_and_authorization_order"]["input_default"] = defaults["aaa_templates"]["authentication_and_authorization_order"]
    if resource == "sdwan_cisco_bgp_feature_template":
        as_number = generated["fields"]["as_number"]
        as_number["input_schema"] = {"anyOf":[{"type":"string"},{"type":"integer","minimum":1,"maximum":4294967295}]}
        as_number["decimal_string"] = True
        for family in ("ipv4", "ipv6"):
            generated["unavailable_inputs"][f"{family}_address_family.neighbors[].optional_variable"] = "Pinned provider model omits neighbor optional_variable"
            generated["unavailable_inputs"][f"{family}_address_family.neighbors[].address_families[].optional_variable"] = "Pinned provider model omits address-family optional_variable"
        generated["unavailable_inputs"]["compare_router_id_variable"] = "Pinned module reads compare_router_idr_variable instead"
    if resource == "sdwan_cisco_secure_internet_gateway_feature_template":
        generated["sig_defaults"] = defaults["secure_internet_gateway_templates"]["interfaces"]
        pair_fields = generated["conditional_trees"][0]["definition"]["trees"]["high_availability_interface_pairs"]["definition"]["fields"]
        for field in ("active_interface", "backup_interface"):
            if pair_fields[field]["schema"] != {"type":"string", "enum":[""]}:
                raise SystemExit("SIG dynamic interface dropdown changed")
            pair_fields[field]["schema"] = {"type":"string", "minLength":1}
    if resource == "sdwan_cisco_logging_feature_template":
        generated["unavailable_inputs"]["tls_profiles[].ciphersuites_variable"] = "Pinned module reads version_ciphersuites_variablevariable instead"
    generated.update(type=template_type, device_types=defaults[public_collection]["device_types"])
    output["templates"][public_collection] = generated
(root / "internal/controlleradapter/sdwan/definitions/classic-templates.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n")
