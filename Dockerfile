FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /exec .

FROM alpine:latest

COPY --from=builder /exec /exec

RUN apk --no-cache add tzdata

ENTRYPOINT ["/exec"]
