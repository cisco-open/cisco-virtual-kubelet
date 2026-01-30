#!/bin/bash
# Check Cisco device readiness for Virtual Kubelet
# Usage: ./check-device.sh <device-ip> <username>

DEVICE_IP=$1
USERNAME=$2

if [ -z "$DEVICE_IP" ] || [ -z "$USERNAME" ]; then
    echo "Usage: $0 <device-ip> <username>"
    exit 1
fi

echo "Cisco Device Readiness Check"
echo "============================="
echo "Device: $DEVICE_IP"
echo ""

# Check RESTCONF connectivity
echo -n "Checking RESTCONF... "
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -k -u "${USERNAME}:${PASSWORD:-cisco}" \
    "https://${DEVICE_IP}/restconf/data/Cisco-IOS-XE-native:native/hostname" 2>/dev/null)

if [ "$HTTP_CODE" = "200" ]; then
    echo "✓ OK"
else
    echo "✗ FAILED (HTTP $HTTP_CODE)"
    echo "  Ensure RESTCONF is enabled: 'restconf' in device config"
fi

# Check IOx status
echo -n "Checking IOx... "
IOX_STATUS=$(curl -s -k -u "${USERNAME}:${PASSWORD:-cisco}" \
    "https://${DEVICE_IP}/restconf/data/Cisco-IOS-XE-native:native/iox" 2>/dev/null)

if echo "$IOX_STATUS" | grep -q "iox"; then
    echo "✓ Enabled"
else
    echo "✗ Not enabled"
    echo "  Enable with: 'iox' in device config"
fi

# Check app-hosting support
echo -n "Checking app-hosting support... "
APP_LIST=$(curl -s -k -u "${USERNAME}:${PASSWORD:-cisco}" \
    "https://${DEVICE_IP}/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data" 2>/dev/null)

if echo "$APP_LIST" | grep -q "app-hosting-oper-data"; then
    echo "✓ Supported"
else
    echo "? Unable to determine"
fi

# Check flash storage
echo ""
echo "Checking flash storage..."
curl -s -k -u "${USERNAME}:${PASSWORD:-cisco}" \
    "https://${DEVICE_IP}/restconf/data/Cisco-IOS-XE-native:native/hostname" 2>/dev/null | head -5

echo ""
echo "Device check complete."
echo ""
echo "If all checks pass, the device is ready for Virtual Kubelet."
