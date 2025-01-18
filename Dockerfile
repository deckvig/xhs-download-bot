# 使用官方的 Golang 镜像作为构建环境
FROM golang:1.23

# 设置工作目录
WORKDIR /app

# 将当前目录的文件复制到 Docker 容器中
COPY . .

# 下载依赖项并编译程序
RUN go mod download && go build .

# 使用 Ubuntu 作为运行时环境
FROM ubuntu:latest

# 安装必要的运行时依赖（如果有）
# 例如，如果你的程序需要网络访问，通常不需要额外安装，但其他依赖可能需要
RUN apt-get update && apt-get install -y ca-certificates && apt-get clean

# 设置工作目录
WORKDIR /root/

# 从构建阶段复制编译好的程序到运行时环境
COPY --from=0 /app/telegram-bot .

# 暴露程序可能监听的端口（如果你的程序是一个服务器）
# EXPOSE 8080  # 根据需要修改

# 运行程序
CMD ["./telegram-bot"]
