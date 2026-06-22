# ---- build stage ----------------------------------------------------------
FROM golang:1.23-alpine AS build
WORKDIR /src

# Cache dependencies separately from source for faster rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static, stripped binary so it runs in a scratch image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/server ./cmd/server

# ---- runtime stage --------------------------------------------------------
# distroless gives us a minimal image with CA certs and a nonroot user,
# nothing else — small attack surface, small image.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/server /server

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
