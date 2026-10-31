# 中心端镜像构建
FROM golang:1.23-alpine AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/netwatch-server ./cmd/server

FROM alpine:3.20
RUN adduser -D -u 10001 netwatch
COPY --from=build /out/netwatch-server /usr/local/bin/netwatch-server
USER netwatch
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["netwatch-server"]
