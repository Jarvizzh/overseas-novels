# =========================================================================
# STAR NOVEL - cms-backend 生产级多阶段 Dockerfile
# =========================================================================

# 1. 编译阶段 (Builder Stage)
FROM golang:1.25-alpine AS builder

WORKDIR /app

# 安装构建依赖与基础 CA 证书
RUN apk add --no-cache git ca-certificates tzdata

# 依赖下载缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制业务源码
COPY . .

# 编译静态二进制程序
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /app/cms-server cmd/server/main.go

# 2. 生产运行阶段 (Minimal Runtime Stage)
FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl
ENV TZ=UTC

COPY --from=builder /app/cms-server /app/cms-server

# 暴露 CMS API 端口
EXPOSE 8081

ENTRYPOINT ["/app/cms-server"]
