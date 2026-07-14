#!/bin/bash
set -e

echo "=== OSRM Map Scheduler ==="
echo "Runs update on the 1st of every month at 3:00 AM"

# Write crontab: 1st of every month at 3:00 AM
echo "0 3 1 * * /usr/local/bin/update-map.sh >> /var/log/map-update.log 2>&1" | crontab -

echo "Cron scheduled. Starting crond..."
crond -f -l 2
