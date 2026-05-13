#!/bin/sh
# Docksmith Sample App - hello.sh
# Demonstrates ENV variable usage and override capability

echo "=================================="
echo "  Docksmith Sample Application"
echo "=================================="
echo ""
echo "$GREETING from inside the container!"
echo ""
echo "--- Container Info ---"
echo "Hostname: $(hostname)"
echo "Working directory: $(pwd)"
echo "User: $(whoami 2>/dev/null || echo 'unknown')"
echo ""
echo "--- Data File Contents ---"
if [ -f /app/data.txt ]; then
    cat /app/data.txt
else
    echo "(data.txt not found)"
fi
echo ""
echo "--- Environment Variables ---"
echo "GREETING=$GREETING"
echo ""
echo "Container execution completed successfully."
