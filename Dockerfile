FROM golang:latest AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -a -installsuffix cgo -o refaas ./cmd/refaas

FROM golang:latest

WORKDIR /app
COPY --from=builder /app/refaas .

EXPOSE 8080
CMD ["./refaas"]