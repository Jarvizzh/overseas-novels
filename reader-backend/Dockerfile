# =========================================================================
# STAR NOVEL - reader-backend 极速轻量运行镜像 (基于预编译 Linux 二进制)
# =========================================================================
FROM alpine:3.19

WORKDIR /app

# 安装基础运行时依赖 (CA 证书、时区、健康检查 curl)
RUN apk add --no-cache ca-certificates tzdata curl
ENV TZ=UTC

# 复制在本地预编译好的 Linux 静态二进制文件
COPY reader-server-linux-amd64 /app/server
RUN chmod +x /app/server

# 暴露读者端 API 端口
EXPOSE 8080

# 容器启动入口
ENTRYPOINT ["/app/server"]
