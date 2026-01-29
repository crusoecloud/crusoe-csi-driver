package crusoe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"k8s.io/klog/v2"
)

const (
	nfsFlagRouteTemplate                     = "%s/projects/%s/storage/nfs/is-using-nfs"
	vastUseSecondaryClusterFlagRouteTemplate = "%s/projects/%s/storage/nfs/vast-use-secondary-cluster"
)

var (
	errCreateFlagRequest = errors.New("failed to create flag request")
	errGetFlag           = errors.New("failed to get flag")
	errReadFlagResponse  = errors.New("failed to read flag response")
	errUnmarshalFlag     = errors.New("failed to unmarshal flag response")
)

type NfsFlagResponse struct {
	Status bool `json:"status"`
}

// getFlag is a helper function to fetch a boolean flag from the API.
func getFlag(ctx context.Context, crusoeHTTPClient *http.Client, flagRoute string) (bool, error) {
	klog.Infof("Fetching flag from URL: %s", flagRoute)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, flagRoute, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errCreateFlagRequest, err)
	}
	resp, err := crusoeHTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errGetFlag, err)
	}

	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errReadFlagResponse, err)
	}

	klog.Infof("Flag API response - Status: %d, Content-Type: %s, Body length: %d bytes",
		resp.StatusCode, resp.Header.Get("Content-Type"), len(bodyBytes))
	klog.Infof("Flag API raw response body: %q", string(bodyBytes))

	// Check HTTP status code before unmarshaling
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: HTTP %d: %s", errGetFlag, resp.StatusCode, string(bodyBytes))
	}

	var flagResponse NfsFlagResponse

	unmarshalErr := json.Unmarshal(bodyBytes, &flagResponse)
	if unmarshalErr != nil {
		return false, fmt.Errorf("%w: %w (response body: %q)", errUnmarshalFlag, unmarshalErr, string(bodyBytes))
	}

	return flagResponse.Status, nil
}

// GetNFSFlag returns true if the project has NFS enabled.
func GetNFSFlag(ctx context.Context, crusoeHTTPClient *http.Client, apiEndpoint, projectID string) (bool, error) {
	nfsFlagRoute := fmt.Sprintf(nfsFlagRouteTemplate, apiEndpoint, projectID)

	return getFlag(ctx, crusoeHTTPClient, nfsFlagRoute)
}

// GetVastUseSecondaryClusterFlag returns true if the project has the vast-use-secondary-cluster flag enabled.
func GetVastUseSecondaryClusterFlag(
	ctx context.Context,
	crusoeHTTPClient *http.Client,
	apiEndpoint, projectID string,
) (bool, error) {
	flagRoute := fmt.Sprintf(vastUseSecondaryClusterFlagRouteTemplate, apiEndpoint, projectID)

	return getFlag(ctx, crusoeHTTPClient, flagRoute)
}
