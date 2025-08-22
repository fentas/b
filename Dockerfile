# Final stage - use scratch for minimal image
FROM scratch

# Copy the pre-built binary (provided by GoReleaser)
COPY b /b

# Copy CA certificates for HTTPS requests from a minimal alpine image
COPY --from=alpine:latest /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Set the entrypoint
ENTRYPOINT ["/b"]
