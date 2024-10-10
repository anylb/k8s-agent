FROM golang:1.23 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o agent .

FROM alpine:latest
RUN apk add --no-cache curl ca-certificates kubectl
WORKDIR /root/
COPY --from=builder /app/agent .
ENTRYPOINT ["./agent"]
