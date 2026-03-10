# Build script for deploying to AWS Lambda (Windows PowerShell)
# This script builds the Lambda function for Linux AMD64 architecture

Write-Host "Building Lambda function for AWS..." -ForegroundColor Cyan

# Set environment for Linux AMD64
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

# Build the function
Write-Host "Compiling..." -ForegroundColor Yellow
go build -o test-lambda main.go

if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ Build failed!" -ForegroundColor Red
    exit 1
}
