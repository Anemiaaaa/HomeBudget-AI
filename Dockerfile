# syntax=docker/dockerfile:1
FROM node:24-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/app
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/smoke ./cmd/smoke

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S app && adduser -S -G app app
COPY --from=build /out/app /usr/local/bin/app
COPY --from=build /out/smoke /usr/local/bin/smoke
COPY --from=web-build /src/web/dist /app/web
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/app"]
