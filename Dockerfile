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
# tzdata is required for the timezone setting: ZenTao returns object dates as
# UTC instants, so turning them into calendar dates needs the server's real
# zone. Without it time.LoadLocation("Asia/Shanghai") fails.
# ca-certificates carries the public root store. The upstream ZenTao entry
# point may be an HTTPS address (the public one is a DigiCert certificate), and
# without a root store every upstream call fails with
# "x509: certificate signed by unknown authority". Alpine's base image normally
# ships ca-certificates-bundle already; naming it here stops a future base image
# change from taking the service down.
RUN apk add --no-cache tzdata ca-certificates
COPY --from=builder /app/zentao-mcp /zentao-mcp
EXPOSE 8080
CMD ["/zentao-mcp"]
