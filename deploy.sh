#!/bin/bash
set -e

SERVER="root@217.114.11.79"

echo "🔨 Сборка..."
GOOS=linux GOARCH=amd64 go build -o bot_daemon

echo "📤 Загрузка..."
scp bot_daemon $SERVER:/root/bot_daemon.new

echo "♻️ Перезапуск..."
ssh $SERVER << 'EOF'
systemctl stop tgbot
mv /root/bot_daemon.new /root/bot_daemon
chmod +x /root/bot_daemon
systemctl start tgbot
systemctl --no-pager status tgbot
EOF

echo "✅ Деплой завершён!"