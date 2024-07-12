package driver

import (
	"fmt"
	"os"
)

const (
	SecretPath    = "/etc/secrets"
	AccessKeyName = "crusoe-csi-accesskey"
	//nolint:gosec // we are not hardcoding credentials, just the env var to get them
	SecretKeyName = "crusoe-csi-secretkey"
)

// Kubernetes provides two main ways of injecting secrets into pods:
// 1) Injecting them into environment variables which can be retrieved by the application
// 2) Creating a file '/etc/secrets' which the application can then retrieve

func ReadSecretFromFile(secretName string) (string, error) {
	// Attempt to open the file corresponding to the secret key
	file, err := os.Open(fmt.Sprintf("%s/%s", SecretPath, secretName))
	if err != nil {
		return "", fmt.Errorf("error opening secret file: %w", err)
	}
	defer file.Close()

	// Read the entire file into a byte slice
	data := make([]byte, 0)
	_, err = file.Read(data)
	if err != nil {
		return "", fmt.Errorf("error reading secret file: %w", err)
	}

	secretValue := string(data)

	return secretValue, nil
}

func GetCrusoeAccessKey() string {
	return ReadEnvVar(AccessKeyName)
}

func GetCrusoeSecretKey() string {
	return ReadEnvVar(SecretKeyName)
}
