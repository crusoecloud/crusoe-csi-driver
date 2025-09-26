package crusoe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const nfsFlagRouteTemplate = "%s/projects/%s/storage/nfs/is-using-nfs"

var (
	errCreateNfsFlagRequest = errors.New("failed to create NFS flag request")
	errGetNfsFlag           = errors.New("failed to get NFS flag")
	errReadNfsResponse      = errors.New("failed to read NFS flag response")
	errUnmarshalNfsFlag     = errors.New("failed to unmarshal NFS flag response")
)

type NfsFlagResponse struct {
	Status bool `json:"status"`
}

// GetNFSFlag returns true if the project has NFS enabled.
func GetNFSFlag(ctx context.Context, crusoeHTTPClient *http.Client, apiEndpoint, projectID string) (bool, error) {
	nfsFlagRoute := fmt.Sprintf(nfsFlagRouteTemplate, apiEndpoint, projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nfsFlagRoute, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errCreateNfsFlagRequest, err)
	}
	resp, err := crusoeHTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errGetNfsFlag, err)
	}

	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errReadNfsResponse, err)
	}

	var nfsFlag NfsFlagResponse

	unmarshalErr := json.Unmarshal(bodyBytes, &nfsFlag)
	if unmarshalErr != nil {
		return false, fmt.Errorf("%w: %w", errUnmarshalNfsFlag, unmarshalErr)
	}

	return nfsFlag.Status, nil
}
