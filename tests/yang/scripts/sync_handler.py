import sysrepo
import signal
import os
from lxml import etree # Ensure lxml is installed: pip install lxml

# Configuration
CFG_MODULE = "Cisco-IOS-XE-app-hosting-cfg"
OPER_FILE = "/yang-modules/operational/app_hosting_oper.xml"

def create_oper_xml(apps):
    """Generates the XML string for the operational data model."""
    NS_URI = "http://cisco.com/ns/yang/Cisco-IOS-XE-app-hosting-oper"
    NSMAP = {
        None: NS_URI,
        "app-hosting-ios-xe-oper": NS_URI
    }
    
    # Create the root element with the nsmap
    root = etree.Element("app-hosting-oper-data", nsmap=NSMAP)
    
    for app_cfg in apps:
        app_name = app_cfg.get('application-name')
        app_node = etree.SubElement(root, "app")
        etree.SubElement(app_node, "name").text = app_name
        
        details = etree.SubElement(app_node, "details")
        etree.SubElement(details, "state").text = "RUNNING"
        
        procs = etree.SubElement(app_node, "processes")
        proc = etree.SubElement(procs, "process")
        etree.SubElement(proc, "id").text = "1"
        etree.SubElement(proc, "name").text = "nginx"
        etree.SubElement(proc, "status").text = "up"
        etree.SubElement(proc, "uptime").text = "2025-12-22T12:00:00Z"
        
        stats = etree.SubElement(app_node, "util-stats")
        etree.SubElement(stats, "cpu-util").text = "12"
        etree.SubElement(stats, "memory-util").text = "256"
        
    return etree.tostring(root, pretty_print=True, encoding='unicode')

def module_change_cb(event, req_id, changes, priv):
    if event == "done":
        with sysrepo.SysrepoConnection() as conn:
            with conn.start_session("running") as sess:
                data = sess.get_data(f"/{CFG_MODULE}:*")
                apps = data.get('app-hosting-cfg-data', {}).get('apps', {}).get('app', [])
                
                # Generate XML content
                xml_content = create_oper_xml(apps)
                
                # Write to the watched directory
                with open(OPER_FILE, "w") as f:
                    f.write(xml_content)
                
                print(f"[*] Updated {OPER_FILE} with {len(apps)} apps. Notconf will now auto-load it.")

def main():
    with sysrepo.SysrepoConnection() as conn:
        with conn.start_session("running") as sess:
            sess.subscribe_module_change(CFG_MODULE, None, module_change_cb, done_only=True)
            signal.pause()

if __name__ == "__main__":
    main()
