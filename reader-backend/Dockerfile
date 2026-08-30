# =========================================================================
# STAR NOVEL - reader-backend 生产级多阶段 Dockerfile
# =========================================================================

# 1. 编译阶段 (Builder Stage)
FROM golang:1.25-alpine AS builder

WORKDIR /app

# 安装基础构建依赖与时区数据
RUN apk add --no-cache git ca-certificates tzdata

# 利用 Docker 层缓存加速依赖下载
COPY go.mod go.sum ./
RUN go mod download

# 复制业务源码
COPY . .

# 编译静态二进制程序 (去除调试信息以缩减体积)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /app/server cmd/server/main.go

# 2. 生产运行阶段 (Minimal Runtime Stage)
FROM alpine:3.19

WORKDIR /app

# 安装根证书与时区支持
RUN apk add --no-cache ca-certificates tzdata curl
ENV TZ=UTC

# 从构建镜像中复制编译产物
COPY --from=builder /app/server /app/server

# 暴露读者端 API 端口
EXPOSE 8080

# 容器启动入口
ENTRYPOINT ["/app/server"]
