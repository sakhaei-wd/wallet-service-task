FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /wallet-service ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /wallet-migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /wallet-service /wallet-service
COPY --from=build /wallet-migrate /wallet-migrate
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/wallet-service"]
