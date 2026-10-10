# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o main .
    

FROM alpine:latest
RUN apk add --no-cache libc6-compat

WORKDIR /app
COPY --from=builder /app/main ./main

VOLUME ["/app/data"]

CMD ["./main"]
