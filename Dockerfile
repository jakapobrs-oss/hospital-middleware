# One image holds both programs: the API (/app/api) and the mock Hospital A HIS (/app/mockhis).
# docker-compose picks which one runs through `command`.

# ---- Build stage: compile static binaries ----
FROM golang:1.27-alpine AS build
WORKDIR /src

# Copy the module files first so the download layer stays cached until go.mod or go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockhis ./cmd/mockhis

# ---- Runtime stage: only the binaries, running as an unprivileged user ----
# alpine (rather than scratch) keeps wget for the compose health checks and the CA bundle for HTTPS calls to a real HIS.
FROM alpine:3.22
RUN addgroup -S app && adduser -S -H -G app app
WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=build /out/mockhis /app/mockhis
USER app
EXPOSE 8080
CMD ["/app/api"]
