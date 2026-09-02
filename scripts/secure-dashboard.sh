#!/bin/bash
#
# secure-dashboard.sh
# 6G Network Dashboard - Secure Deployment (Nginx + SSL + Basic Auth + Fail2Ban + UFW)
#

set -e

# ============================================================
# 0. Root check
# ============================================================
if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: This script must be run as root."
    exit 1
fi

# ============================================================
# 1. Configuration variables
# ============================================================
DOMAIN="localhost"
DASHBOARD_PORT=3000
SSL_COUNTRY="IR"
SSL_STATE="Tehran"
SSL_CITY="Tehran"
SSL_ORG="6G-Research"
SSL_UNIT="Network-Lab"

NGINX_DIR="/etc/nginx"
SSL_DIR="${NGINX_DIR}/ssl"
AUTH_DIR="${NGINX_DIR}/auth"
DASHBOARD_DIR="/root/shiraz-permit-network"

# ── اعتبارنامه از متغیر محیطی یا پرسش تعاملی — هرگز رمز را در این فایل hardcode نکنید
# (این فایل در مخزن عمومی است؛ رمز قبلیِ داخل تاریخچه git لو رفته تلقی می‌شود و نباید دوباره استفاده شود)
DASHBOARD_USER="${DASHBOARD_USER:-}"
DASHBOARD_PASS="${DASHBOARD_PASS:-}"
if [ -z "$DASHBOARD_USER" ]; then
    read -rp "Dashboard username: " DASHBOARD_USER
fi
if [ -z "$DASHBOARD_PASS" ]; then
    read -rsp "Dashboard password: " DASHBOARD_PASS; echo
    read -rsp "Repeat password: " DASHBOARD_PASS2; echo
    [ "$DASHBOARD_PASS" != "$DASHBOARD_PASS2" ] && { echo "رمزها یکسان نیستند"; exit 1; }
fi
[ -z "$DASHBOARD_USER" ] || [ -z "$DASHBOARD_PASS" ] && { echo "نام کاربری/رمز خالی است"; exit 1; }

echo "============================================================"
echo " 6G Network Dashboard - Secure Deployment"
echo "============================================================"

# ============================================================
# 1.5 CLEANUP - Remove legacy services, free port, reset auth
# ============================================================
echo "[*] Step 1.5: Cleaning up previous dashboard deployment..."

LEGACY_SERVICES="dashboard.service 6g-dashboard.service shiraz-network.service node-dashboard.service"

for svc in ${LEGACY_SERVICES}; do
    if systemctl list-unit-files --no-legend | grep -q "^${svc}"; then
        echo "    - Stopping and disabling ${svc}"
        systemctl stop "${svc}" 2>/dev/null || true
        systemctl disable "${svc}" 2>/dev/null || true
    fi
done

# Free port 3000 from any lingering process
if fuser "${DASHBOARD_PORT}/tcp" >/dev/null 2>&1; then
    echo "    - Freeing port ${DASHBOARD_PORT}"
    fuser -k "${DASHBOARD_PORT}/tcp" 2>/dev/null || true
    sleep 2
    if fuser "${DASHBOARD_PORT}/tcp" >/dev/null 2>&1; then
        echo "    - WARNING: port ${DASHBOARD_PORT} still in use"
    else
        echo "    - Port ${DASHBOARD_PORT} freed"
    fi
else
    echo "    - Port ${DASHBOARD_PORT} already free"
fi

# Reset authentication from scratch
if [ -f "${AUTH_DIR}/.htpasswd" ]; then
    echo "    - Removing old authentication file (${AUTH_DIR}/.htpasswd)"
    rm -f "${AUTH_DIR}/.htpasswd"
fi

# Remove default Nginx site
rm -f "${NGINX_DIR}/sites-enabled/default"

systemctl daemon-reload
echo "[+] Cleanup complete."

# ============================================================
# 2. Install packages
# ============================================================
echo "[*] Step 2: Installing packages..."
apt-get update -y
apt-get install -y nginx apache2-utils ufw fail2ban openssl

# ============================================================
# 3. SSL certificate (self-signed)
# ============================================================
echo "[*] Step 3: Generating self-signed SSL certificate..."
mkdir -p "${SSL_DIR}"
openssl req -x509 -nodes -days 365 -newkey rsa:4096 \
    -keyout ${SSL_DIR}/dashboard.key \
    -out ${SSL_DIR}/dashboard.crt \
    -subj "/C=${SSL_COUNTRY}/ST=${SSL_STATE}/L=${SSL_CITY}/O=${SSL_ORG}/OU=${SSL_UNIT}/CN=${DOMAIN}"

chmod 600 ${SSL_DIR}/dashboard.key
chmod 644 ${SSL_DIR}/dashboard.crt

# ============================================================
# 4. Basic Authentication (fresh)
# ============================================================
echo "[*] Step 4: Setting up Basic Authentication..."
mkdir -p ${AUTH_DIR}

# -c recreates the file fresh with a single user
htpasswd -cb ${AUTH_DIR}/.htpasswd "${DASHBOARD_USER}" "${DASHBOARD_PASS}"
chmod 640 ${AUTH_DIR}/.htpasswd
chown root:www-data ${AUTH_DIR}/.htpasswd 2>/dev/null || true

# Helper to add more users later
cat > /usr/local/bin/add-dashboard-user << 'EOF'
#!/bin/bash
if [ "$#" -ne 2 ]; then
    echo "Usage: add-dashboard-user <username> <password>"
    exit 1
fi
htpasswd -b /etc/nginx/auth/.htpasswd "$1" "$2"
echo "User $1 added successfully"
EOF
chmod +x /usr/local/bin/add-dashboard-user

# ============================================================
# 5. Nginx configuration
# ============================================================
echo "[*] Step 5: Writing Nginx configuration..."

cat > ${NGINX_DIR}/sites-available/dashboard << NGINX_CONFIG
# 6G Network Dashboard - Nginx Configuration
# Generated by secure-dashboard.sh

# Redirect HTTP to HTTPS
server {
    listen 80;
    server_name localhost;
    return 301 https://\$server_name\$request_uri;
}

# HTTPS Server
server {
    listen 443 ssl http2;
    server_name localhost;

    # SSL Configuration
    ssl_certificate ${SSL_DIR}/dashboard.crt;
    ssl_certificate_key ${SSL_DIR}/dashboard.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 10m;

    # Security Headers
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "no-referrer-when-downgrade" always;

    # Buffer sizes (fix 400 error)
    client_header_buffer_size 16k;
    large_client_header_buffers 4 32k;
    client_max_body_size 10M;

    # Access log
    access_log /var/log/nginx/dashboard-access.log;
    error_log /var/log/nginx/dashboard-error.log;

    # Authentication for all locations
    auth_basic "6G Network Dashboard - Restricted Access";
    auth_basic_user_file ${AUTH_DIR}/.htpasswd;

    # Proxy to Node.js backend (API)
    location /api/ {
        proxy_pass http://localhost:${DASHBOARD_PORT};
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_cache_bypass \$http_upgrade;

        # Timeouts for long-running tests
        proxy_connect_timeout 600s;
        proxy_send_timeout 600s;
        proxy_read_timeout 600s;
    }

    # Proxy everything else to Node.js (avoids /root permission issues)
    location / {
        proxy_pass http://localhost:${DASHBOARD_PORT};
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_cache_bypass \$http_upgrade;
    }
}
NGINX_CONFIG

ln -sf ${NGINX_DIR}/sites-available/dashboard ${NGINX_DIR}/sites-enabled/

# ============================================================
# 6. UFW firewall
# ============================================================
echo "[*] Step 6: Configuring UFW firewall..."
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp comment 'SSH'
ufw allow 80/tcp comment 'HTTP'
ufw allow 443/tcp comment 'HTTPS'
# ufw allow 7050:7060/tcp comment 'Fabric Orderer'
# ufw allow 9440:9447/tcp comment 'Fabric Peers'
ufw --force enable
ufw status numbered

echo ""
echo "⚠️  توجه: Docker قوانین iptables خودش را می‌نویسد و از UFW عبور می‌کند؛"
echo "   پورت‌های publish شده peer/orderer (7050 تا 14051) همچنان از بیرون باز هستند."
echo "   برای بستن‌شان، در config/docker-compose.yml هر port را به loopback مقید کنید:"
echo "     \"7051:7051\"  →  \"127.0.0.1:7051:7051\"   (برای هر ۹ سرویس)"
echo "   سپس: cd /root/shiraz-permit-network/config && docker-compose up -d"

# ============================================================
# 7. Fail2Ban
# ============================================================
echo "[*] Step 7: Configuring Fail2Ban..."
cat > /etc/fail2ban/jail.d/nginx-auth.conf << 'EOF'
[nginx-http-auth]
enabled = true
port = http,https
logpath = /var/log/nginx/*error.log
maxretry = 5
findtime = 600
bantime = 3600
EOF

systemctl restart fail2ban
systemctl enable fail2ban

# ============================================================
# 8. System hardening + systemd service
# ============================================================
echo "[*] Step 8: Hardening and creating systemd service..."
chmod 700 ${DASHBOARD_DIR}/server
chmod 600 ${DASHBOARD_DIR}/server/config.js
chmod 755 ${DASHBOARD_DIR}/public 2>/dev/null || true

cat > /etc/systemd/system/dashboard.service << EOF
[Unit]
Description=6G Network Dashboard Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=${DASHBOARD_DIR}/server
Environment="NODE_ENV=production"
Environment="CRYPTO_BASE=${DASHBOARD_DIR}/config/crypto-config"
ExecStart=/usr/bin/node index.js
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable dashboard.service

# ============================================================
# 9. Logging and monitoring
# ============================================================
echo "[*] Step 9: Configuring logging and monitoring..."
cat > /etc/logrotate.d/dashboard << 'EOF'
/var/log/nginx/dashboard-*.log {
    daily
    missingok
    rotate 14
    compress
    delaycompress
    notifempty
    create 640 www-data adm
    sharedscripts
    postrotate
        [ -f /var/run/nginx.pid ] && kill -USR1 `cat /var/run/nginx.pid`
    endscript
}
EOF

cat > /usr/local/bin/dashboard-status << 'EOF'
#!/bin/bash
echo "=== Dashboard Service ==="
systemctl status dashboard.service --no-pager -l | head -n 15
echo
echo "=== Nginx ==="
systemctl status nginx --no-pager -l | head -n 10
echo
echo "=== Recent Access (last 10) ==="
tail -n 10 /var/log/nginx/dashboard-access.log 2>/dev/null
echo
echo "=== Failed Logins (401) ==="
grep " 401 " /var/log/nginx/dashboard-access.log 2>/dev/null | tail -n 10
echo
echo "=== Fail2Ban ==="
fail2ban-client status nginx-http-auth 2>/dev/null || echo "jail not active"
EOF
chmod +x /usr/local/bin/dashboard-status

# ============================================================
# 10. Start services
# ============================================================
echo "[*] Step 10: Starting services..."
systemctl restart dashboard.service
systemctl restart nginx
systemctl enable nginx

# ============================================================
# 11. Summary
# ============================================================
cat << EOF

============================================================
 Setup Summary
============================================================

Dashboard Access:
  URL:      https://localhost
  Username: ${DASHBOARD_USER}
  Password: (همان که وارد کردید — چاپ نمی‌شود)

Management Commands:
  systemctl status dashboard        - Check service status
  systemctl restart dashboard       - Restart dashboard
  journalctl -u dashboard -f        - View logs
  dashboard-status                  - Full status report
  add-dashboard-user <user> <pass>  - Add new user

Security Features:
  HTTPS/TLS 1.2+ encryption
  HTTP Basic Authentication
  Fail2Ban brute-force protection
  UFW firewall configured
  Security headers enabled

Log Files:
  Access:  /var/log/nginx/dashboard-access.log
  Error:   /var/log/nginx/dashboard-error.log
  Service: journalctl -u dashboard -f

Important:
  - Self-signed certificate is in use (browser warning is normal)
  - Change default password: htpasswd /etc/nginx/auth/.htpasswd ${DASHBOARD_USER}
  - Test endpoint: https://localhost/api/health
  - All requests are now proxied to Node.js on port ${DASHBOARD_PORT}
    (port ${DASHBOARD_PORT} was freed during cleanup)
============================================================
EOF

# Connectivity test
sleep 2
curl -sk -u ${DASHBOARD_USER}:${DASHBOARD_PASS} \
    https://localhost/api/health | jq '.' 2>/dev/null \
    || echo "Dashboard is starting..."

echo "✓ Setup complete. Dashboard is secured and running."
