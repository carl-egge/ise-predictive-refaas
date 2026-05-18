FROM golang:latest AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -a -installsuffix cgo -o refaas .

FROM golang:latest

WORKDIR /app
COPY --from=builder /app/refaas .
COPY --from=builder /app/default.yaml .

EXPOSE 8080
CMD ["./refaas"]