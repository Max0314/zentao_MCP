FROM golang:1.24-alpine AS builder
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -o zentao-mcp cmd/app/*

FROM alpine:3.21
COPY --from=builder /app/zentao-mcp /zentao-mcp
EXPOSE 8080
CMD ["/zentao-mcp"]
