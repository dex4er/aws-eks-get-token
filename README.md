# aws-eks-get-token

[![Go Version](https://img.shields.io/github/go-mod/go-version/dex4er/aws-eks-get-token)](https://golang.org/)
[![License](https://img.shields.io/github/license/dex4er/aws-eks-get-token)](LICENSE)
[![Release](https://img.shields.io/github/v/release/dex4er/aws-eks-get-token)](https://github.com/dex4er/aws-eks-get-token/releases)

A fast, native Go replacement for `aws eks get-token` with advanced caching and configuration options.

## Features

- 🚀 **Native AWS SDK Integration** - No external AWS CLI dependency
- 📦 **Token Caching** - Intelligent caching with configurable TTL
- 🔧 **Drop-in Replacement** - Compatible with `aws eks get-token` arguments
- 🌍 **Environment Variable Support** - Full AWS environment variable compatibility
- 🔐 **Client Certificate Support** - Inject client certificates for mutual TLS
- ⚡ **High Performance** - Single binary with no external dependencies
- 🎯 **Flexible Configuration** - Command-line flags with environment variable fallbacks

## Installation

### Binary Releases

Download the latest binary from the [releases page](https://github.com/dex4er/aws-eks-get-token/releases).

### From Source

```bash
go install github.com/dex4er/aws-eks-get-token@latest
```

### Using Make

```bash
git clone https://github.com/dex4er/aws-eks-get-token.git
cd aws-eks-get-token
make install
```

## Usage

### Basic Usage

```bash
# Generate token for EKS cluster
aws-eks-get-token --cluster-name my-cluster --region us-west-2

# Using cluster ARN
aws-eks-get-token --cluster-id arn:aws:eks:us-west-2:123456789012:cluster/my-cluster --region us-west-2

# With specific AWS profile
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --profile my-profile

# Force refresh (ignore cache)
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --ignore-cache
```

### Drop-in AWS CLI Replacement

```bash
# This works as a direct replacement for aws eks get-token
aws-eks-get-token eks get-token --cluster-name my-cluster --region us-west-2

# Arguments are automatically filtered
aws-eks-get-token --cluster-name my-cluster --region us-west-2
```

### Advanced Configuration

```bash
# Custom cache directory
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --cache-dir ~/my-cache

# Custom token TTL (in seconds)
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --ttl 3600

# Assume IAM role
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --role-arn arn:aws:iam::123456789012:role/EKSRole

# Regional STS endpoints
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --sts-regional-endpoints regional

# Client certificate injection
aws-eks-get-token --cluster-name my-cluster --region us-west-2 \
  --client-cert-file client.crt --client-key-file client.key
```

## Configuration Options

### Command Line Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--cluster-name` | EKS cluster name | Required* |
| `--cluster-id` | EKS cluster ID (ARN or ID) | Required* |
| `--region` | AWS region | From env vars |
| `--profile` | AWS profile | From env vars |
| `--role-arn` | IAM role ARN to assume | None |
| `--ignore-cache` | Ignore cached tokens | `false` |
| `--cache-dir` | Custom cache directory | `~/.kube/cache/tokens` |
| `--ttl` | Token TTL in seconds | `900` (15 min) |
| `--output` | Output format (`json` or default) | Default |
| `--client-cert-file` | Client certificate file path | From env vars |
| `--client-key-file` | Client key file path | From env vars |
| `--sts-regional-endpoints` | STS endpoint type (`regional` or `legacy`) | `regional` |

*Either `--cluster-name` or `--cluster-id` is required (mutually exclusive)

### Environment Variables

The tool supports all standard AWS environment variables:

| Environment Variable | Description | Flag Override |
|---------------------|-------------|---------------|
| `AWS_REGION` | AWS region | `--region` |
| `AWS_DEFAULT_REGION` | AWS region (fallback) | `--region` |
| `AWS_PROFILE` | AWS profile | `--profile` |
| `AWS_STS_REGIONAL_ENDPOINT` | STS endpoint type | `--sts-regional-endpoints` |
| `CLIENT_CERT_FILE` | Client certificate file | `--client-cert-file` |
| `CLIENT_KEY_FILE` | Client key file | `--client-key-file` |

### Precedence Order

Configuration values are resolved in this order (highest to lowest priority):

1. Command line flags
2. Environment variables
3. Default values

## Caching

### How Caching Works

- Tokens are cached in `~/.kube/cache/tokens/` by default
- Cache files are named: `{cluster-name}_{region}.json`
- Cached tokens are automatically validated for expiration
- Client certificates are never cached (security)

### Cache Management

```bash
# Use custom cache directory
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --cache-dir /tmp/eks-cache

# Ignore cache for this request
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --ignore-cache

# Custom TTL affects both token expiration and cache duration
aws-eks-get-token --cluster-name my-cluster --region us-west-2 --ttl 3600
```

## Examples

### Kubernetes Configuration

Add to your `~/.kube/config`:

```yaml
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTi...
    server: https://ABCDEF1234567890.gr7.us-west-2.eks.amazonaws.com
  name: my-cluster
contexts:
- context:
    cluster: my-cluster
    user: my-cluster
  name: my-cluster
current-context: my-cluster
kind: Config
users:
- name: my-cluster
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws-eks-get-token
      args:
      - --cluster-name
      - my-cluster
      - --region
      - us-west-2
```

### CI/CD Pipeline

```bash
#!/bin/bash
# Set custom cache directory for CI
export KUBECONFIG=/tmp/kubeconfig
aws-eks-get-token --cluster-name production --region us-west-2 --cache-dir /tmp/eks-cache
kubectl get nodes
```

### Multi-Environment Setup

```bash
# Development
AWS_PROFILE=dev aws-eks-get-token --cluster-name dev-cluster --region us-west-2

# Production
AWS_PROFILE=prod aws-eks-get-token --cluster-name prod-cluster --region us-west-2 --role-arn arn:aws:iam::123456789012:role/ProdEKSRole
```

## Troubleshooting

### Common Issues

1. **Authentication Errors**
   ```bash
   # Check AWS credentials
   aws sts get-caller-identity
   
   # Use specific profile
   aws-eks-get-token --cluster-name my-cluster --region us-west-2 --profile my-profile
   ```

2. **Cluster Not Found**
   ```bash
   # Verify cluster exists and region is correct
   aws eks describe-cluster --name my-cluster --region us-west-2
   ```

3. **Permission Denied**
   ```bash
   # Ensure your AWS credentials have EKS permissions
   # Required permissions: eks:DescribeCluster
   ```

4. **Cache Issues**
   ```bash
   # Clear cache and try again
   rm -rf ~/.kube/cache/tokens/
   aws-eks-get-token --cluster-name my-cluster --region us-west-2
   ```

### Debug Mode

For verbose output, check the AWS SDK configuration:

```bash
export AWS_SDK_LOAD_CONFIG=1
export AWS_LOG_LEVEL=debug
aws-eks-get-token --cluster-name my-cluster --region us-west-2
```

## Comparison with AWS CLI

| Feature | aws-eks-get-token | aws eks get-token |
|---------|-------------------|-------------------|
| Dependencies | None (single binary) | AWS CLI + Python |
| Performance | ~50ms | ~500ms+ |
| Caching | Built-in with TTL | None |
| Client Certificates | ✅ | ❌ |
| Custom Cache Dir | ✅ | ❌ |
| STS Regional Endpoints | ✅ | ✅ |
| Drop-in Compatible | ✅ | N/A |

## Development

### Building

```bash
# Build for current platform
make build

# Build for all platforms
goreleaser build --clean --snapshot

# Install locally
make install
```

### Dependencies

- [AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2)
- [Cobra CLI](https://github.com/spf13/cobra)
- [Smithy Go](https://github.com/aws/smithy-go)

### Testing

```bash
# Run with your own cluster
./aws-eks-get-token --cluster-name your-cluster --region your-region --ignore-cache

# Test caching
./aws-eks-get-token --cluster-name your-cluster --region your-region
./aws-eks-get-token --cluster-name your-cluster --region your-region  # Should use cache
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Inspired by the AWS CLI's `eks get-token` command
- Built for the Kubernetes and AWS communities
- Thanks to all contributors and users
