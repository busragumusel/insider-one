FROM golang:1.22-alpine AS base
WORKDIR /src
RUN apk add --no-cache ca-certificates tzdata

FROM base AS deps
COPY go.mod go.sum ./
RUN go mod download

FROM deps AS test
COPY . .
RUN go test ./...

FROM deps AS build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/insider-one ./cmd/insider-one

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates tzdata && addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=build /out/insider-one /usr/local/bin/insider-one
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/insider-one"]