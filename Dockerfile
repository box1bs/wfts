FROM golang:1.26-alpine AS builder
RUN apk add --no-cache make
WORKDIR /app
COPY . .
RUN make build

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/.bin/wfts .
COPY model .
COPY scaler .
COPY configs/default.json .
VOLUME [ "/app/.data" ]
EXPOSE 8080
CMD ["./wfts"]