#!/bin/bash
# WhatsApp bridge — keeps the whatsmeow connection alive
# First run: displays QR code in terminal for pairing
umask 077
cd "$(dirname "$0")/whatsapp-bridge" || exit 1
exec ./whatsapp-bridge
