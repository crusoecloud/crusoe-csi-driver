package crusoe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

	var flagResponse NfsFlagResponse

	unmarshalErr := json.Unmarshal(bodyBytes, &flagResponse)
	if unmarshalErr != nil {
		return false, fmt.Errorf("%w: %w", errUnmarshalFlag, unmarshalErr)
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
