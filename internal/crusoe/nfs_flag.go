package crusoe

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

const nfsFlagRouteTemplate = "%s/projects/%s/storage/nfs/is-using-nfs"

type NfsFlagResponse struct {
	Status bool `json:"status"`
}

var customClient *http.Client
var customClientLock sync.Mutex

// GetNFSFlag returns true if the project has NFS enabled
func GetNFSFlag(apiEndpoint string, projectID string, apiKey string, apiSecret string) (bool, error) {
	customClientLock.Lock()
	if customClient == nil {
		customClient = &http.Client{}
		customClient.Transport = NewAuthenticatingTransport(nil, apiKey, apiSecret)
	}
	customClientLock.Unlock()

	nfsFlagRoute := fmt.Sprintf(nfsFlagRouteTemplate, apiEndpoint, projectID)
	resp, err := customClient.Get(nfsFlagRoute)
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
