 # Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o cisco-vk ./cmd/virtual-kubelet

FROM gcr.io/distroless/static-debian12
COPY --from=builder /app/cisco-vk /usr/local/bin/cisco-vk
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/cisco-vk"] 