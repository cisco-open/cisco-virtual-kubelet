#!/bin/bash
# Upload container image to Cisco device
# Usage: ./upload-image.sh <device-ip> <username> <image.tar>

set -e

DEVICE_IP=$1
USERNAME=$2
IMAGE_PATH=$3

if [ -z "$DEVICE_IP" ] || [ -z "$USERNAME" ] || [ -z "$IMAGE_PATH" ]; then
    echo "Usage: $0 <device-ip> <username> <image.tar>"
    echo ""
    echo "Example: $0 192.168.1.100 admin nginx.tar"
    exit 1
fi

if [ ! -f "$IMAGE_PATH" ]; then
    echo "Error: Image file not found: $IMAGE_PATH"
    exit 1
fi

IMAGE_NAME=$(basename "$IMAGE_PATH")
IMAGE_SIZE=$(stat -f%z "$IMAGE_PATH" 2>/dev/null || stat -c%s "$IMAGE_PATH")
IMAGE_SIZE_MB=$((IMAGE_SIZE / 1024 / 1024))

echo "Cisco Device Image Upload"
echo "========================="
echo "Device: $DEVICE_IP"
echo "Image: $IMAGE_NAME ($IMAGE_SIZE_MB MB)"
echo ""

echo "Uploading to flash:/$IMAGE_NAME..."
scp -O "$IMAGE_PATH" "${USERNAME}@${DEVICE_IP}:flash:/${IMAGE_NAME}"

echo ""
echo "✓ Upload complete!"
echo ""
echo "Verify on device with:"
echo "  ssh ${USERNAME}@${DEVICE_IP} \"dir flash: | include ${IMAGE_NAME}\""
