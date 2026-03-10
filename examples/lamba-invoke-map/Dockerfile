
# Runtime stage - use AWS Lambda runtime interface emulator compatible base
FROM  alpine:latest

# Copy the binary from builder
ADD --chmod=0777 ./test-lambda ./test-lambda

# Set the CMD to your handler
ENTRYPOINT [ "./test-lambda" ]

