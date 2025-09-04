package crusoe

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const nfsFlagRouteTemplate = "%s/projects/%s/storage/nfs/is-using-nfs"

type NfsFlagResponse struct {
	Status bool `json:"status"`
}

// GetNFSFlag returns true if the project has NFS enabled
func GetNFSFlag(crusoeHTTPClient *http.Client, apiEndpoint string, projectID string) (bool, error) {
	nfsFlagRoute := fmt.Sprintf(nfsFlagRouteTemplate, apiEndpoint, projectID)
	resp, err := crusoeHTTPClient.Get(nfsFlagRoute)
	if err != nil {
		return false, err
	}

	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var nfsFlag NfsFlagResponse
	if err = json.Unmarshal(bodyBytes, &nfsFlag); err != nil {
		return false, err
	}

	return nfsFlag.Status, nil
}
