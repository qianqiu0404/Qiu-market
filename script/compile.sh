#!/bin/bash
set -e

# 检查是否安装了 Go
if ! command -v go &> /dev/null; then
    echo "Error: Go is not installed" >&2
    exit 1
fi

# 检查是否安装了 protoc（没有则给出安装提示）
if ! command -v protoc &> /dev/null; then
    echo "Error: protoc is not installed" >&2
    echo "Install it first, e.g.: brew install protobuf   (macOS)" >&2
    echo "                 sudo apt install protobuf-compiler   (Debian/Ubuntu)" >&2
    exit 1
fi

# GOPATH 未设置时以 go env 为准（而不是写死 $HOME/go）
if [ -z "$GOPATH" ]; then
    export GOPATH=$(go env GOPATH)
    echo "GOPATH was not set, using: $GOPATH"
fi

# 创建必要的目录
mkdir -p "$GOPATH/bin"

# 添加 GOBIN 到 PATH，保证 protoc 能找到插件
export PATH="$PATH:$GOPATH/bin"

# 只在缺失时安装 protoc 插件，避免每次重新生成都走网络拉 @latest
if ! command -v protoc-gen-go &> /dev/null; then
    echo "Installing protoc-gen-go..."
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
fi

if ! command -v protoc-gen-go-grpc &> /dev/null; then
    echo "Installing protoc-gen-go-grpc..."
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
fi

echo "Compiling protobuf files..."
protoc -I ./ \
    --go_out=./ \
    --go-grpc_out=./ \
    protobuf/*.proto

if [ $? -eq 0 ]; then
    echo "Compilation completed successfully"
else
    echo "Compilation failed" >&2
    exit 1
fi
