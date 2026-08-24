FROM golang:latest AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -a -installsuffix cgo -o refaas ./cmd/refaas

FROM golang:latest

# CPython for internal/pyscan's source analyzer ([C8]/[I3]). Go has no
# Python parser, and an approximate tokenizer would put training-time and
# serving-time feature values on different bases. The analyzer imports only
# the standard library, so the bare interpreter is enough - no pip, no
# requirements file.
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/refaas .

EXPOSE 8080
CMD ["./refaas"]
