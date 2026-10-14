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
    known = {"const", "type", "enum", "minimum", "maximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "minProperties", "maxProperties", "items", "anyOf", "oneOf", "uniqueItems", "default", "description", "additionalProperties", "x-attributeMetadata", "metadata", "format", "properties", "required", "allOf", "if", "then", "else", "dependencies", "dependentRequired", "not"}
    if set(schema) - known:
        raise SystemExit(f"Unsupported validation keywords: {set(schema) - known}")
    if schema.get("format") not in (None, "ipv4", "ipv6", "hostname"):
        raise SystemExit("Unsupported schema format")
    if "items" in schema:
        validate_schema(schema["items"])
    for dependency in (schema.get("dependencies", {}) | schema.get("dependentRequired", {})).values():
        if isinstance(dependency, dict):
            validate_schema(dependency)
        elif not isinstance(dependency, list) or not all(isinstance(x, str) for x in dependency):
            raise SystemExit("Unsupported schema dependency")
    for child in schema.get("properties", {}).values():
        validate_schema(child)
    for key in ("if", "then", "else", "not"):
        if key in schema:
            validate_schema(schema[key])
    for item in schema.get("anyOf", []) + schema.get("oneOf", []) + schema.get("allOf", []):
        validate_schema(item)

def property_schema(node, name):
    if name in node.get("properties", {}):
        return node["properties"][name]
    candidates = [branch["properties"][name] for branch in node.get("oneOf", [])
                  if name in branch.get("properties", {})]
    if candidates and all(x == candidates[0] for x in candidates):
        return candidates[0]
    if len(candidates) > 1 and all(x.get("properties", {}).get("optionType", {}).get("enum") == ["global"] for x in candidates):
        return {"oneOf": candidates}
    if len(candidates) > 1 and key.startswith(("service_routing_", "transport_routing_")):
        return {"oneOf":candidates}
    if len(candidates) > 1 and key == "service_multicast" and all("optionType" in x.get("properties", {}) and "value" in x.get("properties", {}) for x in candidates):
        return {"oneOf":candidates}
    if len(candidates) != 1:
        raise SystemExit(f"Ambiguous or missing schema property {name} in {key}")
    return candidates[0]

def repair_sse_schema(node):
    """The pinned Cisco SSE dpdInterval schema nests required in properties."""
    if isinstance(node, dict):
        properties = node.get("properties", {})
        if isinstance(properties.get("required"), list):
            misplaced = properties.pop("required")
            if misplaced != ["optionType", "value"] or "required" in node:
                raise SystemExit("Unreviewed SSE schema correction")
            node["required"] = misplaced
        for value in node.values():
            repair_sse_schema(value)
    elif isinstance(node, list):
        for value in node:
            repair_sse_schema(value)

def definition(source, alias, indent, model, body, schema, receiver="data", target="body", struct_name=None):
    struct_match = re.search(r"type " + (re.escape(struct_name) if struct_name else r"\w+") + r" struct \{", model)
    struct_body = block(model, struct_match.end()-1)
    fields = {tf: (name, typ) for name, typ, tf in re.findall(r'(\w+)\s+types\.(\w+)\s+`tfsdk:"([^"]+)"`', struct_body)}
    prefix = r'path\+' if target == "body" else ''
    setter = r'sjson.Set\(' + target + ', ' + prefix
    baseline, inputs, lists, required, public_defaults, wrappers = {}, {}, {}, [], {}, []
    constants, computed_defaults, wrapped_variables = {}, {}, set()
    encodings, references, transforms = {}, {}, {}
    for match in re.finditer(r'if ' + receiver + r'\.(\w+)\.IsNull\(\) \{', body):
        content = block(body, match.end() - 1)
        for path, literal in re.findall(setter + r'"([^"]+)", ([^\n]+)\)', content):
            put(baseline, path, {} if literal == "map[string]interface{}{}" else json.loads(literal))
    for match in re.finditer(r'^\t+if true \{' if key == 'sse_cisco' else r'^\tif true \{', body, re.M):
        content = block(body, match.end() - 1)
        if 'for ' not in content and receiver + '.' not in content:
            for path, literal in re.findall(setter + r'"([^"]+)", ([^\n]+)\)', content):
                put(baseline, path, {} if literal == "map[string]interface{}{}" else json.loads(literal))
    assignments = re.findall('^' + ' ' * indent + r'(\w+)\s*=\s*(.*)$', source, re.M)
    for tf, expr in assignments:
        if indent == 2 and tf in {"for_each", "name", "description", "feature_profile_id"}:
            continue
        if expr.split("#")[0].strip() == "null":
            continue
        if re.fullmatch(r'"[^"]*"', expr):
            inputs[tf] = "$constant:" + tf
            constants[tf] = json.loads(expr)
            continue
        if tf == "tls_decryption_action":
            expected = 'lookup({ "never_decrypt" = "neverDecrypt", "skip_decrypt" = "skipDecrypt" }, each.value.tls_action, each.value.tls_action)'
            if expr != expected:
                raise SystemExit("Unreviewed TLS action expression")
            inputs[tf] = "tls_action"
            transforms[tf] = {"encode":"enum-map", "values":{"never_decrypt":"neverDecrypt", "skip_decrypt":"skipDecrypt", "decrypt":"decrypt"}, "input_schema":{"type":"string", "enum":["never_decrypt", "skip_decrypt", "decrypt"]}}
            required.append("tls_action")
            continue
        if tf == "block_page_contents":
            expected = 'each.value.block_page_action == "text" ? try(each.value.block_page_content_body, null) != null ? "Access to the requested page has been denied. ${each.value.block_page_content_body}" : "Access to the requested page has been denied. ${local.defaults.sdwan.feature_profiles.policy_object_profile.security_url_filtering_profiles.block_page_content_body}" : null'
            if expr != expected:
                raise SystemExit("Unreviewed URL block-page expression")
            inputs[tf] = "block_page_content_body"
            public_defaults["block_page_content_body"] = module_defaults["defaults"]["sdwan"]["feature_profiles"]["policy_object_profile"]["security_url_filtering_profiles"]["block_page_content_body"]
            transforms[tf] = {"prefix":"Access to the requested page has been denied. ", "condition_field":"block_page_action", "condition_value":"text"}
            continue
        routing_ref = re.fullmatch(r'try\(sdwan_((?:service|transport)_route_policy)_feature\.\w+\["\$\{each.value.profile.name\}-\$\{' + re.escape(alias) + r'\.(\w+)\}"\]\.id, null\)', expr)
        if routing_ref:
            target_kind, public = routing_ref.groups()
            references[tf] = ("route-policy", target_kind)
            inputs[tf] = public
            continue
        reference = re.fullmatch(r'(?:try\()?sdwan_(policy_object_\w+)\.\w+\[' + re.escape(alias) + r'\.(\w+)\]\.id(?:, null\))?', expr)
        if reference:
            reference_target, public = reference.groups()
            target_model = (args.provider / "internal/provider" / ("model_sdwan_" + reference_target + ".go")).read_text()
            references[tf] = (re.search(r'/policy-object/%v/([^" ]+)', target_model).group(1), reference_target)
            inputs[tf] = public
            if not expr.startswith("try("):
                required.append(public)
            continue
        prefix_part = re.fullmatch(r'split\("/", ' + re.escape(alias) + r'(?:\.(\w+))?\)\[([01])\]', expr)
        decimal = re.fullmatch(r'tostring\(' + re.escape(alias) + r'\.(\w+)\)', expr)
        if prefix_part:
            public = prefix_part.group(1) or "$value"
            inputs[tf] = public
            required.append(public)
            encodings[tf] = "prefix-address" if prefix_part.group(2) == "0" else "prefix-length"
            continue
        if decimal:
            inputs[tf] = decimal.group(1)
            required.append(decimal.group(1))
            encodings[tf] = "decimal-string"
            continue
        computed = False
        for group_prefix in ("radius", "tacacs"):
            expected = 'try(' + alias + '.group_name, "' + group_prefix + '-${index}-${' + alias + '.vpn}", "' + group_prefix + '-${index}-${index}")'
            if expr == expected:
                inputs[tf] = "group_name"
                computed_defaults["group_name"] = group_prefix
                computed = True
        if computed:
            continue
        if expr in (alias, "try(" + alias + ", null)"):
            inputs[tf] = "$value"
            continue
        match = re.fullmatch(r'try\(' + re.escape(alias) + r'\.(\w+), null\)', expr)
        fallback = re.fullmatch(r'try\(' + re.escape(alias) + r'\.(\w+), local.defaults.([\w.]+)(?:, null)?\)', expr)
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
            if variable:
                wrapped_variables.add(tf)
            continue
        direct = re.fullmatch(re.escape(alias) + r'\.(\w+)', expr)
        if direct:
            inputs[tf] = direct.group(1)
            required.append(direct.group(1))
            continue
        array = re.search(r'\[for (?:index, )?(\w+) in (?:try\()?' + re.escape(alias) + r'\.(\w+)(?:, \[\]\))? : \{', expr)
        conditional_wrapper = re.fullmatch(r'try\(length\(' + re.escape(alias) + r'\.(\w+)\) == 0, true\) \? null : \[\{', expr)
        if not array and expr != "[{" and not conditional_wrapper:
            raise SystemExit(f"Unsupported expression {tf}: {expr}")
        item_alias, public = array.groups() if array else (alias, None)
        item_source = block(source, source.index("{", source.index(expr) + (array.start() if array else 0)))
        go_name, child_struct = re.search(r'(\w+)\s+\[\](\w+)\s+`tfsdk:"' + tf + '"`', struct_body).groups()
        loop = re.search(r'for _, (\w+) := range ' + receiver + r'\.' + go_name + r' \{', body)
        item_receiver = loop.group(1)
        item_body = block(body, loop.end() - 1)
        item_target = re.search(r'(\w+) := ""', item_body).group(1)
        array_paths = re.findall(setter + r'"([^"]+)", \[\]interface\{\}\{\}\)', body[:loop.start()])
        array_path = re.search(r'sjson.SetRaw\(' + target + r', ' + prefix + r'"([^" ]+)\.-1", ' + item_target + r'\)', body[loop.start():]).group(1)
        array_paths = [p for p in array_paths if p == array_path]
        node = schema
        for part in array_path.split("."):
            node = property_schema(node, part)
        entry = {"path": array_path, "definition": definition(item_source, item_alias, indent + 2, model, item_body, (node["items"] if "items" in node else {"oneOf":[x["items"] for x in node["oneOf"]]}), item_receiver, item_target, child_struct)}
        if array:
            if expr.startswith("[for") and " in " + alias + "." in expr:
                required.append(public)
            entry["primitive"] = "$value" in entry["definition"]["fields"]
            entry["omit_empty"] = not bool(array_paths)
            lists[public] = entry
        else:
            if conditional_wrapper:
                entry["nonempty"] = conditional_wrapper.group(1)
            wrappers.append(entry)
        if array_paths:
            put(baseline, array_path, [])
    mapping = {}
    for tf, public in inputs.items():
        if tf.endswith("_variable"):
            continue
        name, typ = fields[tf]
        paths = re.findall(setter + r'"([^"]+)\.value", ' + receiver + r'\.' + name + r'\.Value\w+\(\)\)', body)
        if typ in ("Set", "List"):
            match = re.search(receiver + r'\.' + name + r'\.ElementsAs[^\n]*\n\s*' + target + ', _ = ' + setter + r'"([^"]+)\.value", values\)', body)
            paths = [match.group(1)] if match else []
        if len(set(paths)) != 1:
            raise SystemExit(f"Ambiguous or missing API path {tf}")
        path = paths[0]
        node = schema
        for part in path.split("."):
            node = property_schema(node, part)
        global_values = [x["properties"]["value"] for x in node.get("oneOf", [node]) if "global" in x["properties"]["optionType"].get("enum", [x["properties"]["optionType"].get("const")])]
        if not global_values:
            raise SystemExit(f"Missing global value schema {tf}")
        global_schema = global_values[0] if len(global_values) == 1 else {"anyOf": global_values}
        validate_schema(global_schema)
        if tf in constants:
            put(baseline, path, {"optionType": "global", "value": constants[tf]})
            continue
        source_public = public
        if public in mapping:
            public = "$field:" + tf
        mapping[public] = {"path": path, "type": typ, "schema": global_schema}
        if public != source_public:
            mapping[public]["source"] = source_public
        if tf in transforms:
            mapping[public].update(transforms[tf])
        if tf in references:
            mapping[public]["reference"], mapping[public]["reference_kind"] = references[tf]
        if tf in encodings:
            mapping[public]["encode"] = encodings[tf]
            mapping[public]["input_schema"] = {"type":"integer", "minimum":0} if encodings[tf] == "decimal-string" else {"type":"string"}
        if tf + "_variable" in inputs:
            mapping[public]["variable"] = inputs[tf + "_variable"]
            if tf + "_variable" not in wrapped_variables:
                mapping[public]["variable_encoding"] = "verbatim"
    return {"defaults": baseline, "fields": mapping, "lists": lists, "required": required, "public_defaults": public_defaults, "wrappers": wrappers, "computed_defaults": computed_defaults}

output = {"license": "MPL-2.0", "provider_commit": lock["provider"]["commit"], "module_commit": lock["module"]["commit"], "parcels": {}}
reviewed = [("system", f, f, f) for f in ["global", "omp", "basic", "ntp", "logging", "security", "snmp", "aaa"]]
reviewed += [("transport", "gps", "gps", "transport_gps"),
             ("other", "trustsec", "trustsec", "other_trustsec"), ("other", "ucse", "ucse", "other_ucse"),
             ("other", "thousandeyes", "thousandeyes", "other_thousandeyes")]
reviewed += [("service", "tracker", "tracker", "service_tracker"), ("service", "dhcp_server", "dhcp_server", "service_dhcp_server"), ("service", "object_tracker", "tracker", "service_object_tracker")]
reviewed += [("transport", "cellular_controller", "controller", "transport_cellular_controller"), ("transport", "cellular_profile", "cellular", "transport_cellular_profile")]
reviewed += [("service", "tracker_group", "tracker", "service_tracker_group"), ("service", "object_tracker_group", "tracker", "service_object_tracker_group")]
policy_features = ["as_path_list", "class_map", "color_list", "extended_community_list",
                   "mirror", "policer", "security_data_ipv4_prefix_list", "security_fqdn_list",
                   "security_local_domain_list", "security_port_list", "security_protocol_list",
                   "security_url_allow_list", "security_url_block_list", "standard_community_list", "tloc_list", "expanded_community_list", "data_ipv4_prefix_list", "data_ipv6_prefix_list", "ipv4_prefix_list", "ipv6_prefix_list", "security_ips_signature", "unified_advanced_malware_protection", "app_probe_class", "sla_class_list", "unified_intrusion_prevention", "unified_advanced_inspection_profile", "unified_url_filtering", "preferred_color_group", "application_list", "security_local_application_list", "security_geolocation_list", "security_zone"]
reviewed += [("policy_object", f, "", "policy_object_" + f) for f in policy_features]
reviewed += [("transport", f, "tracker", "transport_" + f) for f in ("tracker", "ipv6_tracker", "tracker_group", "ipv6_tracker_group")]
reviewed += [("sse", "cisco", "cisco", "sse_cisco"), ("system", "ca_certificate", "ca_certificate", "ca_certificate")]
reviewed += [(family, feature, "ospf", family+"_"+feature) for family, feature in [("service","routing_ospf"),("service","routing_ospfv3_ipv4"),("service","routing_ospfv3_ipv6"),("transport","routing_ospf")]]
reviewed += [("service", "routing_eigrp", "eigrp", "service_routing_eigrp"), ("sse", "zscaler", "zscaler", "sse_zscaler"), ("service", "switchport", "switchport", "service_switchport")]
reviewed += [("service", "multicast", "multicast", "service_multicast")]
for family, feature, alias, key in reviewed:
    hcl = (args.module / ("sdwan_policy_objects.tf" if family == "policy_object" else "sdwan_features_" + family + ".tf")).read_text()
    resource = "sdwan_" + family + "_" + feature + ("" if family == "policy_object" else "_feature")
    start = hcl.index("{", hcl.index('resource "' + resource + '"'))
    source = block(hcl, start)
    combined = {}
    combinations = {
        "policy_object_application_list": [("applications", "application"), ("application_families", "application_family")],
        "policy_object_security_local_application_list": [("applications", "app"), ("application_families", "app_family")],
        "policy_object_security_geolocation_list": [("continent_codes", "continent"), ("country_codes", "country")],
        "policy_object_security_zone": [("vpns", "vpn"), ("interfaces", "interface")],
    }
    if key in combinations:
        # These four module declarations concatenate independent public arrays
        # into one API entries array. Preserve both arrays and their source order.
        start_entries = source.index("  entries =")
        source = source[:start_entries] + "  entries = [for e in each.value.entries : {\n" + "".join("    " + field + " = try(e." + field + ", null)\n" for _, field in combinations[key]) + "  }]\n"
        combined = {"entries":[{"source":public,"field":field} for public,field in combinations[key]]}
    if key == "ca_certificate":
        source = source.replace("certificates :\n    {", "certificates : {")
        source = re.sub(r"^      (trust_point_name|ca_certificate_id)", r"    \1", source, flags=re.M)
    if key in ("service_tracker_group", "service_object_tracker_group", "transport_tracker_group", "transport_ipv6_tracker_group"):
        target = key.removesuffix("_group")
        reference = 'sdwan_' + target + '_feature.' + target + '_feature["${each.value.profile.name}-${t}"].id'
        if source.count(reference) != 1:
            raise SystemExit("Tracker reference expression changed")
        source = source.replace(reference, 't')
    if key in ("transport_tracker", "transport_ipv6_tracker"):
        source = source.replace('try(each.value.tracker.endpoint_ip, each.value.tracker.endpoint_tcp_udp_ip, null)', 'try(each.value.tracker.endpoint_ip, null)')
        ip_prefix = "ipv6-" if key == "transport_ipv6_tracker" else ""
        expected = 'lookup({ "icmp" = "' + ip_prefix + 'interface-icmp", "http" = "' + ip_prefix + 'interface" }, try(each.value.tracker.endpoint_tracker_type, ""), null)'
        if source.count(expected) != 1:
            raise SystemExit("Transport tracker discriminator changed")
        source = source.replace(expected, 'try(each.value.tracker.endpoint_tracker_type, null)')
        for kind in ("icmp", "http"):
            guard = 'try(each.value.tracker.endpoint_tracker_type, "http") == "' + kind + '" ? '
            lines = source.splitlines()
            for i, line in enumerate(lines):
                if guard in line:
                    if not line.endswith(' : null'):
                        raise SystemExit("Transport interval condition changed")
                    lines[i] = line.replace(guard, '')[:-len(' : null')]
            source = "\n".join(lines)
    if key == "transport_cellular_profile":
        toggle_line = r'^  requires_authentication\s*= try\(each.value.cellular.authentication_enable, null\)\n'
        source, count = re.subn(toggle_line, '', source, flags=re.M)
        if count != 1:
            raise SystemExit("Cellular authentication toggle changed")
    if key == "service_object_tracker":
        expr = 'each.value.tracker.type == "sig" ? "SIG" : title(each.value.tracker.type)'
        if source.count(expr) != 1:
            raise SystemExit("Object tracker type expression changed")
        source = source.replace(expr, "each.value.tracker.type")
    if key == "service_switchport":
        expr = 'length(try(interface.trunk_allowed_vlans, [])) > 0 ? join(",", try(interface.trunk_allowed_vlans, [])) : null'
        if source.count(expr) != 1:
            raise SystemExit("Service switchport VLAN join changed")
        source = source.replace(expr, 'try(interface.trunk_allowed_vlans, null)')
    if key == "snmp":
        # Upstream typo is unreachable for a string-valued group.view. Do not
        # expose a supposedly working variable field that the module cannot use.
        typo = 'try("{{${group.view.view_variable}}}", null)'
        if source.count(typo) != 1:
            raise SystemExit("SNMP upstream compatibility exception changed")
        source = source.replace(typo, "null")
    model = (args.provider / "internal/provider" / ("model_" + resource + ".go")).read_text()
    body = model[model.index("toBody(ctx"):model.index("return body")]
    schema = json.loads((args.provider / "gen/models/profile_parcels" / (family + "_" + feature + ".json")).read_text())["request"]["properties"]["data"]
    if key == "sse_cisco":
        repair_sse_schema(schema)
    generated = definition(source, "each.value" + ("." + alias if alias else ""), 2, model, body, schema)
    if combined:
        generated["combined_lists"] = combined
        generated["required"] = [x for x in generated["required"] if x != "entries"]
    if key == "service_object_tracker":
        generated["fields"]["type"].update(input_schema={"type":"string", "enum":["interface", "route", "sig"]}, encode="enum-map", values={"interface":"Interface", "route":"Route", "sig":"SIG"})
    if key == "transport_cellular_profile":
        guard = 'if true && data.RequiresAuthentication.ValueBool() == false {'
        if body.count(guard) != 1 or body.count('if true && data.RequiresAuthentication.ValueBool() == true {') != 6:
            raise SystemExit("Cellular authentication guards changed")
        content = block(body, body.index(guard) + len(guard) - 1)
        off = {}
        for path, literal in re.findall(r'sjson.Set\(body, path\+"([^"]+)", ([^\n]+)\)', content):
            put(off, path, json.loads(literal))
        generated["fields"]["slice_differentiator"]["requires"] = "slice_type"
        generated["toggles"] = {"authentication_enable": {"off_defaults": off, "fields": ["authentication_type", "profile_username", "profile_password"]}}
    if key == "policy_object_unified_url_filtering":
        generated["fields"]["redirect_url"].update(condition_field="block_page_action", condition_value="redirect-url")
        generated["fields"]["alerts"].update(condition_field="enable_alerts", condition_value=True)
    if key == "policy_object_unified_advanced_malware_protection":
        for field in ("tg_cloud_region", "file_analysis_alert_log_level", "file_analysis_file_types"):
            generated["fields"][field].update(condition_field="file_analysis", condition_value=True)
    if key == "sse_zscaler":
        # Manager 20.18.2.2 rejects these omissions even though the pinned
        # provider's request schema marks them optional.
        if "tracker_source_ip" not in generated["fields"] or "tunnel_source_interface" not in generated["lists"]["interfaces"]["definition"]["fields"]:
            raise SystemExit("Zscaler Manager-required fields changed")
        generated["required"].append("tracker_source_ip")
        generated["lists"]["interfaces"]["definition"]["required"].append("tunnel_source_interface")
    if key == "service_switchport":
        field = generated["lists"]["interfaces"]["definition"]["fields"]["trunk_allowed_vlans"]
        field.update(encode="comma-join", input_schema={"type":"array", "items":{"type":"integer", "minimum":1, "maximum":4094}})
    if key in ("transport_tracker", "transport_ipv6_tracker"):
        generated["fields"]["endpoint_ip"]["alternate"] = "endpoint_tcp_udp_ip"
        ip_prefix = "ipv6-" if key == "transport_ipv6_tracker" else ""
        generated["fields"]["endpoint_tracker_type"].update(encode="enum-map", values={"http":ip_prefix+"interface", "icmp":ip_prefix+"interface-icmp"}, input_schema={"type":"string", "enum":["http", "icmp"]})
        for field in generated["fields"].values():
            if field["path"] in ("interval", "icmpInterval"):
                field.update(condition_field="endpoint_tracker_type", condition_value="http" if field["path"] == "interval" else "icmp", condition_default="http")
    existing_policy = {"as_path_list", "class_map", "color_list", "extended_community_list", "mirror", "policer", "security_data_ipv4_prefix_list", "security_fqdn_list", "security_local_domain_list", "security_port_list", "security_protocol_list", "security_url_allow_list", "security_url_block_list", "standard_community_list", "tloc_list"}
    if family == "service" or key.startswith("transport_routing_") or key.startswith("transport_cellular") or key in ("transport_tracker", "transport_ipv6_tracker", "transport_tracker_group", "transport_ipv6_tracker_group") or key in ("sse_cisco", "ca_certificate") or (family == "policy_object" and feature not in existing_policy):
        validate_schema(schema)
        generated["output_schema"] = schema
    if key == "snmp":
        generated["unavailable_inputs"] = {"groups[].view_variable": "Pinned module reads group.view.view_variable although group.view is a string"}
    if family == "policy_object":
        generated["collection"] = re.search(r'feature_profiles.policy_object_profile\.(\w+)', source).group(1)
        generated["path"] = re.search(r'/policy-object/%v/([^" ]+)', model).group(1)
        generated["ignore_description"] = not bool(re.search(r'description\s*=\s*try\(each.value.description', source))
    # Reviewed NaC numeric fields whose provider schema encodes strings.
    # Public constraints: netascode.cisco.com/docs/data_models/sdwan/policy_objects/{class_map,tloc_list}/
    if key == "policy_object_class_map":
        field = generated["wrappers"][0]["definition"]["fields"]["queue"]
        field.update(input_schema={"type": "integer", "minimum": 0, "maximum": 7}, encode="decimal-string")
    if key == "policy_object_tloc_list":
        field = generated["lists"]["tlocs"]["definition"]["fields"]["preference"]
        field.update(input_schema={"type": "integer", "minimum": 0, "maximum": 4294967295}, encode="decimal-string")
    output["parcels"][key] = generated
folder = root / "internal/controlleradapter/sdwan/definitions"
folder.mkdir(exist_ok=True)
(folder / "scalar-parcels.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n")
(folder / "device-access-schemas.json").write_text(json.dumps({
    ip: json.loads((args.provider / "gen/models/profile_parcels" / f"system_{ip}_device_access.json").read_text())["request"]["properties"]["data"]
    for ip in ("ipv4", "ipv6")
}, indent=2, sort_keys=True) + "\n")
(folder / "topology-mesh-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/topology_mesh.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "topology-hubspoke-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/topology_hubspoke.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "ipv4-acl-schemas.json").write_text(json.dumps({
    family: json.loads((args.provider / "gen/models/profile_parcels" / f"{family}_ipv4_acl.json").read_text())["request"]["properties"]["data"]
    for family in ("service", "transport")
}, indent=2, sort_keys=True) + "\n")
(folder / "ipv6-acl-schemas.json").write_text(json.dumps({
    family: json.loads((args.provider / "gen/models/profile_parcels" / f"{family}_ipv6_acl.json").read_text())["request"]["properties"]["data"]
    for family in ("service", "transport")
}, indent=2, sort_keys=True) + "\n")
(folder / "route-policy-schemas.json").write_text(json.dumps({
    family: json.loads((args.provider / "gen/models/profile_parcels" / f"{family}_route_policy.json").read_text())["request"]["properties"]["data"]
    for family in ("service", "transport")
}, indent=2, sort_keys=True) + "\n")
(folder / "transport-vpn-schemas.json").write_text(json.dumps({
    kind: json.loads((args.provider / "gen/models/profile_parcels" / f"transport_{kind}_vpn.json").read_text())["request"]["properties"]["data"]
    for kind in ("management", "wan")
}, indent=2, sort_keys=True) + "\n")
(folder / "transport-management-interface-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/transport_management_vpn_interface_ethernet.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "service-lan-vpn-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/service_lan_vpn.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "service-dual-router-ha-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/service_dual_router_ha.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "service-svi-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/service_lan_vpn_interface_svi.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "service-ethernet-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/service_lan_vpn_interface_ethernet.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "service-gre-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/service_lan_vpn_interface_gre.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "transport-gre-schema.json").write_text(json.dumps(json.loads(
    (args.provider / "gen/models/profile_parcels/transport_wan_vpn_interface_gre.json").read_text()
)["request"]["properties"]["data"], indent=2, sort_keys=True) + "\n")
(folder / "LICENSE").write_text((args.provider / "LICENSE").read_text())
