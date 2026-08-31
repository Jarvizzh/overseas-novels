#!/bin/bash
set -e

# 获取脚本所在根目录
PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "=========================================================="
echo "🚀 开始在本地 Mac 交叉编译 Star Novel Linux 生产二进制文件"
echo "=========================================================="

echo "📦 [1/2] 正在编译 reader-backend -> reader-server-linux-amd64 ..."
cd "$PROJECT_ROOT/reader-backend"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o reader-server-linux-amd64 cmd/server/main.go
echo "✅ reader-server-linux-amd64 编译完成: $(ls -lh reader-server-linux-amd64 | awk '{print $5}')"

echo "📦 [2/2] 正在编译 cms-backend -> cms-server-linux-amd64 ..."
cd "$PROJECT_ROOT/cms-backend"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o cms-server-linux-amd64 cmd/server/main.go
echo "✅ cms-server-linux-amd64 编译完成: $(ls -lh cms-server-linux-amd64 | awk '{print $5}')"

echo "=========================================================="
echo "🎉 全部 Linux 生产二进制文件编译成功！"
echo "=========================================================="
