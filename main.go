package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/spf13/cobra"
)

const (
	defaultTTL     = 900 // 15 minutes in seconds
	presignExpires = 60  // 1 minute in seconds
)

// EKSTokenResponse represents the structure returned by AWS EKS get-token
type EKSTokenResponse struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Status     struct {
		ExpirationTimestamp   string `json:"expirationTimestamp"`
		Token                 string `json:"token"`
		ClientCertificateData string `json:"clientCertificateData,omitempty"`
		ClientKeyData         string `json:"clientKeyData,omitempty"`
	} `json:"status"`
}

// Params holds configuration parameters for EKS token generation
type Params struct {
	ClusterName         string
	ClusterId           string
	Region              string
	Profile             string
	RoleArn             string
	IgnoreCache         bool
	ClientCertFile      string
	ClientKeyFile       string
	TTL                 int
	Output              string
	STSRegionalEndpoint string
	CacheDir            string
}

func main() {
	params := Params{}

	var rootCmd = &cobra.Command{
		Use:   "aws-eks-get-token",
		Short: "Generate and cache EKS authentication tokens",
		Long: `A CLI tool to generate authentication tokens for Amazon EKS clusters with caching support.
This tool caches tokens to improve performance and reduce API calls. It also supports client certificate injection.`,
		SilenceErrors: true,
		Example: `  aws-eks-get-token --cluster-name my-cluster --region us-west-2
  aws-eks-get-token --cluster-id arn:aws:eks:us-west-2:123456789012:cluster/my-cluster --region us-west-2
  aws-eks-get-token --cluster-name my-cluster --region us-west-2 --profile my-profile
  aws-eks-get-token --cluster-name my-cluster --region us-west-2 --output json
  aws-eks-get-token --cluster-name my-cluster --region us-west-2 --sts-regional-endpoints regional
  aws-eks-get-token --cluster-name my-cluster --region us-west-2 --cache-dir /tmp/eks-tokens
  AWS_PROFILE=my-profile aws-eks-get-token --cluster-name my-cluster --region us-west-2
  aws-eks-get-token --cluster-name my-cluster --region us-west-2 --client-cert-file client.crt --client-key-file client.key
  CLIENT_CERT_FILE=client.crt CLIENT_KEY_FILE=client.key aws-eks-get-token --cluster-name my-cluster --region us-west-2
  AWS_STS_REGIONAL_ENDPOINT=legacy aws-eks-get-token --cluster-name my-cluster --region us-west-2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for client cert files from environment variables if not provided via flags
			if params.ClientCertFile == "" {
				if envCertFile := os.Getenv("CLIENT_CERT_FILE"); envCertFile != "" {
					params.ClientCertFile = envCertFile
				}
			}
			if params.ClientKeyFile == "" {
				if envKeyFile := os.Getenv("CLIENT_KEY_FILE"); envKeyFile != "" {
					params.ClientKeyFile = envKeyFile
				}
			}

			// Expand ~ to home directory for client cert and key files
			if params.ClientCertFile != "" {
				params.ClientCertFile = expandHomeDir(params.ClientCertFile)
			}
			if params.ClientKeyFile != "" {
				params.ClientKeyFile = expandHomeDir(params.ClientKeyFile)
			}

			return getEKSToken(params)
		},
	}

	rootCmd.Flags().StringVar(&params.ClusterName, "cluster-name", "", "EKS cluster name")
	rootCmd.Flags().StringVar(&params.ClusterId, "cluster-id", "", "EKS cluster ID (ARN or ID)")
	rootCmd.Flags().StringVar(&params.Region, "region", "", "AWS region (optional if AWS_REGION or AWS_DEFAULT_REGION is set)")
	rootCmd.Flags().StringVar(&params.Profile, "profile", "", "Use a specific profile from your credential file (optional if AWS_PROFILE is set)")
	rootCmd.Flags().StringVar(&params.RoleArn, "role-arn", "", "Assume a role ARN when getting the token")
	rootCmd.Flags().BoolVar(&params.IgnoreCache, "ignore-cache", false, "Ignore cached token")
	rootCmd.Flags().StringVar(&params.ClientCertFile, "client-cert-file", "", "Path to client certificate file (optional if CLIENT_CERT_FILE is set)")
	rootCmd.Flags().StringVar(&params.ClientKeyFile, "client-key-file", "", "Path to client key file (optional if CLIENT_KEY_FILE is set)")
	rootCmd.Flags().IntVar(&params.TTL, "ttl", defaultTTL, "Token TTL in seconds")
	rootCmd.Flags().StringVar(&params.Output, "output", "", "Output format (json or omit for default)")
	rootCmd.Flags().StringVar(&params.STSRegionalEndpoint, "sts-regional-endpoints", "", "Use regional STS endpoints (regional or legacy, optional if AWS_STS_REGIONAL_ENDPOINT is set)")
	rootCmd.Flags().StringVar(&params.CacheDir, "cache-dir", "", "Override default cache directory (~/.kube/cache/tokens)")

	// Custom validation for cluster name or ID
	rootCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if params.ClusterName == "" && params.ClusterId == "" {
			return fmt.Errorf("either --cluster-name or --cluster-id must be specified")
		}
		if params.ClusterName != "" && params.ClusterId != "" {
			return fmt.Errorf("--cluster-name and --cluster-id are mutually exclusive")
		}

		// Check for region from environment variables if not provided via flag
		if params.Region == "" {
			if envRegion := os.Getenv("AWS_REGION"); envRegion != "" {
				params.Region = envRegion
			} else if envRegion := os.Getenv("AWS_DEFAULT_REGION"); envRegion != "" {
				params.Region = envRegion
			} else {
				return fmt.Errorf("region must be specified via --region flag or AWS_REGION/AWS_DEFAULT_REGION environment variable")
			}
		}

		// Check for profile from environment variable if not provided via flag
		if params.Profile == "" {
			if envProfile := os.Getenv("AWS_PROFILE"); envProfile != "" {
				params.Profile = envProfile
			}
		}

		// Check for STS regional endpoint from environment variable if not provided via flag
		if params.STSRegionalEndpoint == "" {
			if envSTSEndpoint := os.Getenv("AWS_STS_REGIONAL_ENDPOINT"); envSTSEndpoint != "" {
				params.STSRegionalEndpoint = envSTSEndpoint
			} else {
				// Default to "regional" if neither flag nor env var is set
				params.STSRegionalEndpoint = "regional"
			}
		}

		// Validate STS regional endpoint if specified
		if params.STSRegionalEndpoint != "" && params.STSRegionalEndpoint != "regional" && params.STSRegionalEndpoint != "legacy" {
			return fmt.Errorf("invalid sts-regional-endpoints value '%s'. Valid values are 'regional' or 'legacy'", params.STSRegionalEndpoint)
		}

		// Validate output format if specified
		if params.Output != "" && params.Output != "json" {
			return fmt.Errorf("invalid output format '%s'. Only 'json' is supported or omit for default", params.Output)
		}

		// Validate that cache-dir and ignore-cache are mutually exclusive
		if params.CacheDir != "" && params.IgnoreCache {
			return fmt.Errorf("--cache-dir and --ignore-cache are mutually exclusive")
		}

		return nil
	}

	// Filter out "eks get-token" arguments to allow drop-in replacement for aws eks get-token
	filteredArgs := filterEksGetTokenArgs(os.Args[1:])
	rootCmd.SetArgs(filteredArgs)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// filterEksGetTokenArgs removes "eks" and "get-token" from the argument list
// This allows the tool to be used as a drop-in replacement for "aws eks get-token"
func filterEksGetTokenArgs(args []string) []string {
	var filtered []string

	for i, arg := range args {
		// Skip "eks" if it's the first argument or follows certain patterns
		if arg == "eks" && (i == 0 || (i > 0 && args[i-1] != "--")) {
			continue
		}
		// Skip "get-token" if it follows "eks" or is standalone
		if arg == "get-token" {
			continue
		}
		filtered = append(filtered, arg)
	}

	return filtered
}

func expandHomeDir(path string) string {
	if len(path) > 0 && path[0] == '~' {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(homeDir, path[1:])
	}
	return path
}

func getCacheFilePath(params Params) (string, error) {
	var cacheDir string

	if params.CacheDir != "" {
		// Use custom cache directory
		cacheDir = expandHomeDir(params.CacheDir)
	} else {
		// Use default cache directory
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		cacheDir = filepath.Join(homeDir, ".kube", "cache", "tokens")
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Use cluster_region format like the bash script
	var clusterIdentifier string
	if params.ClusterId != "" {
		// If cluster-id is an ARN, extract the cluster name from it
		// Format: arn:aws:eks:region:account-id:cluster/cluster-name
		if len(params.ClusterId) > 0 && params.ClusterId[0:4] == "arn:" {
			parts := strings.Split(params.ClusterId, "/")
			if len(parts) > 0 {
				clusterIdentifier = parts[len(parts)-1]
			} else {
				// Fallback to using the full cluster-id (might be long)
				clusterIdentifier = params.ClusterId
			}
		} else {
			// Direct cluster ID
			clusterIdentifier = params.ClusterId
		}
	} else {
		clusterIdentifier = params.ClusterName
	}

	cacheFileName := fmt.Sprintf("%s_%s.json", clusterIdentifier, params.Region)
	return filepath.Join(cacheDir, cacheFileName), nil
}

func readCachedToken(cacheFile string) (*EKSTokenResponse, error) {
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}

	var token EKSTokenResponse
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}

	return &token, nil
}

func isTokenValid(token *EKSTokenResponse) bool {
	expiration, err := time.Parse(time.RFC3339, token.Status.ExpirationTimestamp)
	if err != nil {
		return false
	}

	now := time.Now()
	return expiration.After(now)
}

func writeCachedToken(cacheFile string, token *EKSTokenResponse) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cacheFile, data, 0600)
}

func fetchNewToken(params Params) (*EKSTokenResponse, error) {
	ctx := context.Background()

	// Load AWS configuration
	cfg, err := loadAWSConfig(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create EKS client
	eksClient := eks.NewFromConfig(cfg)

	// Determine cluster identifier
	var clusterName string
	if params.ClusterId != "" {
		clusterName = params.ClusterId
	} else {
		clusterName = params.ClusterName
	}

	// Call EKS GetToken API
	input := &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	}

	// First, get cluster info to ensure it exists
	_, err = eksClient.DescribeCluster(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe cluster: %w", err)
	}

	// Generate the token using the same logic as AWS CLI
	// The token is a base64-encoded STS GetCallerIdentity request
	token, expiration, err := generateEKSToken(cfg, clusterName, params.TTL)
	if err != nil {
		return nil, fmt.Errorf("failed to generate EKS token: %w", err)
	}

	// Create the response in the same format as AWS CLI
	response := &EKSTokenResponse{
		Kind:       "ExecCredential",
		APIVersion: "client.authentication.k8s.io/v1beta1",
		Status: struct {
			ExpirationTimestamp   string `json:"expirationTimestamp"`
			Token                 string `json:"token"`
			ClientCertificateData string `json:"clientCertificateData,omitempty"`
			ClientKeyData         string `json:"clientKeyData,omitempty"`
		}{
			ExpirationTimestamp: expiration.Format(time.RFC3339),
			Token:               token,
		},
	}

	return response, nil
}

func loadAWSConfig(ctx context.Context, params Params) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error

	// Set region
	opts = append(opts, config.WithRegion(params.Region))

	// Set profile if specified
	if params.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(params.Profile))
	}

	// Set STS regional endpoint if specified
	// In AWS SDK v2, we set this via environment variable internally
	if params.STSRegionalEndpoint != "" {
		os.Setenv("AWS_STS_REGIONAL_ENDPOINTS", params.STSRegionalEndpoint)
	}

	// Load base configuration
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}

	// Handle role assumption if specified
	if params.RoleArn != "" {
		stsClient := sts.NewFromConfig(cfg)
		creds := stscreds.NewAssumeRoleProvider(stsClient, params.RoleArn)
		cfg.Credentials = creds
	}

	return cfg, nil
}

func generateEKSToken(cfg aws.Config, clusterName string, ttl int) (string, time.Time, error) {
	ctx := context.Background()

	// Base STS client
	stsClient := sts.NewFromConfig(cfg)

	// Create middleware to inject x-k8s-aws-id (this will be signed)
	clusterHeader := smithyhttp.SetHeaderValue("x-k8s-aws-id", clusterName)

	// Also inject X-Amz-Expires as an API option so the presigner uses desired expiry
	expiresHeader := smithyhttp.SetHeaderValue("X-Amz-Expires", strconv.Itoa(presignExpires))

	// Build a PresignClient that includes the API options by default
	// (WithPresignClientFromClientOptions takes client options like WithAPIOptions)
	presignClient := sts.NewPresignClient(stsClient,
		sts.WithPresignClientFromClientOptions(sts.WithAPIOptions(clusterHeader, expiresHeader)),
	)

	// Presign a GetCallerIdentity request (no input params required)
	presigned, err := presignClient.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to presign GetCallerIdentity:", err)
		os.Exit(1)
	}

	// The token format is "k8s-aws-v1." + base64url(raw-presigned-url)
	token := "k8s-aws-v1." + base64.RawURLEncoding.EncodeToString([]byte(presigned.URL))

	// expiration timestamp: now + requested expires seconds
	expiration := time.Now().UTC().Add(time.Duration(ttl) * time.Second)

	// Remove padding manually
	token = strings.TrimRight(token, "=")

	return token, expiration, nil
}

func addClientCertificates(token *EKSTokenResponse, params Params) error {
	if params.ClientCertFile != "" {
		certData, err := os.ReadFile(params.ClientCertFile)
		if err != nil {
			return fmt.Errorf("failed to read client certificate file: %w", err)
		}
		token.Status.ClientCertificateData = string(certData)
	}

	if params.ClientKeyFile != "" {
		keyData, err := os.ReadFile(params.ClientKeyFile)
		if err != nil {
			return fmt.Errorf("failed to read client key file: %w", err)
		}
		token.Status.ClientKeyData = string(keyData)
	}

	return nil
}

func outputToken(token *EKSTokenResponse) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}

	fmt.Println(string(data))
	return nil
}

func getEKSToken(params Params) error {
	if !params.IgnoreCache {
		// Get cache file path
		cacheFile, err := getCacheFilePath(params)
		if err != nil {
			return fmt.Errorf("failed to get cache file path: %w", err)
		}

		// Check if we have valid cached token
		if cachedToken, err := readCachedToken(cacheFile); err == nil {
			if isTokenValid(cachedToken) {
				// Add client certificates if needed
				if params.ClientCertFile != "" || params.ClientKeyFile != "" {
					if err := addClientCertificates(cachedToken, params); err != nil {
						return fmt.Errorf("failed to add client certificates: %w", err)
					}
				}
				return outputToken(cachedToken)
			}
		}
	}

	// Fetch new token
	token, err := fetchNewToken(params)
	if err != nil {
		return fmt.Errorf("failed to fetch new token: %w", err)
	}

	// Add client certificates if needed
	if params.ClientCertFile != "" || params.ClientKeyFile != "" {
		if err := addClientCertificates(token, params); err != nil {
			return fmt.Errorf("failed to add client certificates: %w", err)
		}
	}

	// Cache the token (without client certificates to avoid storing sensitive data)
	tokenToCache := *token
	tokenToCache.Status.ClientCertificateData = ""
	tokenToCache.Status.ClientKeyData = ""

	if !params.IgnoreCache {
		cacheFile, err := getCacheFilePath(params)
		if err != nil {
			return fmt.Errorf("failed to get cache file path: %w", err)
		}

		if err := writeCachedToken(cacheFile, &tokenToCache); err != nil {
			// Don't fail if we can't cache, just continue
			fmt.Fprintf(os.Stderr, "Warning: failed to cache token: %v\n", err)
		}
	}

	return outputToken(token)
}
