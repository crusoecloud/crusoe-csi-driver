package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/antihax/optional"

	swagger "github.com/crusoecloud/client-go/swagger/v1alpha4"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

const (
	pollInterval          = 2 * time.Second
	OpSucceeded  opStatus = "SUCCEEDED"
	OpInProgress opStatus = "IN_PROGRESS"
	OpFailed     opStatus = "FAILED"
)

// apiError models the error format returned by the Crusoe API go client.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type opStatus string

type opResultError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var (
	errUnableToGetOpRes = errors.New("failed to get result of operation")
	// fallback error presented to the user in unexpected situations.
	errUnexpected = errors.New("an unexpected error occurred, please try again, and if the problem persists, " +
		"contact support@crusoecloud.com")
	errBadFQDN          = errors.New("fqdn in unexpected format")
	errInstanceNotFound = errors.New("instance not found")
)

// UnpackAPIError takes a swagger API error and safely attempts to extract any additional information
// present in the response. The original error is returned unchanged if it cannot be unpacked.
func UnpackAPIError(original error) error {
	apiErr := &swagger.GenericSwaggerError{}
	if ok := errors.As(original, apiErr); !ok {
		return original
	}

	var model apiError
	err := json.Unmarshal(apiErr.Body(), &model)
	if err != nil {
		return original
	}

	// some error messages are of the format "rpc code = ... desc = ..."
	// in those cases, we extract the description and return it
	const two = 2
	components := strings.Split(model.Message, " desc = ")
	if len(components) == two {
		//nolint:goerr113 // error is dynamic
		return fmt.Errorf("%s", components[1])
	}

	//nolint:goerr113 // error is dynamic
	return fmt.Errorf("%s", model.Message)
}

func opResultToError(res interface{}) (expectedErr, unexpectedErr error) {
	b, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal operation error: %w", err)
	}
	resultError := opResultError{}
	err = json.Unmarshal(b, &resultError)
	if err != nil {
		return nil, fmt.Errorf("op result type not error as expected: %w", err)
	}

	//nolint:goerr113 //This function is designed to return dynamic errors
	return fmt.Errorf("%s", resultError.Message), nil
}

func parseOpResult[T any](opResult interface{}) (*T, error) {
	b, err := json.Marshal(opResult)
	if err != nil {
		return nil, errUnableToGetOpRes
	}

	var result T
	err = json.Unmarshal(b, &result)
	if err != nil {
		return nil, errUnableToGetOpRes
	}

	return &result, nil
}

// awaitOperation polls an async API operation until it resolves into a success or failure state.
func awaitOperation(ctx context.Context, op *crusoeapi.Operation, projectID string,
	getFunc func(context.Context, string, string) (crusoeapi.Operation, *http.Response, error)) (
	*crusoeapi.Operation, error,
) {
	for op.State == string(OpInProgress) {
		updatedOps, httpResp, err := getFunc(ctx, projectID, op.OperationId)
		if err != nil {
			return nil, fmt.Errorf("error getting operation with id %s: %w", op.OperationId, err)
		}
		httpResp.Body.Close()

		op = &updatedOps

		time.Sleep(pollInterval)
	}

	switch op.State {
	case string(OpSucceeded):
		return op, nil
	case string(OpFailed):
		opError, err := opResultToError(op.Result)
		if err != nil {
			return op, err
		}

		return op, opError
	default:

		return op, errUnexpected
	}
}

// AwaitOperationAndResolve awaits an async API operation and attempts to parse the response as an instance of T,
// if the operation was successful.
func awaitOperationAndResolve[T any](ctx context.Context, op *crusoeapi.Operation, projectID string,
	getFunc func(context.Context, string, string) (crusoeapi.Operation, *http.Response, error),
) (*T, *crusoeapi.Operation, error) {
	op, err := awaitOperation(ctx, op, projectID, getFunc)
	if err != nil {
		return nil, op, err
	}

	result, err := parseOpResult[T](op.Result)
	if err != nil {
		return nil, op, err
	}

	return result, op, nil
}

func GetInstanceID(ctx context.Context, client *crusoeapi.APIClient) (
	instanceID string,
	projectID string,
	location string,
	err error,
) {
	// FQDN is of the form: <vm-name>.<location>.compute.internal
	fqdn := GetNodeFQDN()

	fqdnSlice := strings.Split(fqdn, ".")
	if len(fqdnSlice) < 1 {
		return "", "", "", errBadFQDN
	}

	vmName := fqdnSlice[0]

	instance, err := findInstance(ctx, client, vmName)
	if err != nil {
		return "", "", "", fmt.Errorf("could not find instance (%s): %w", vmName, err)
	}

	return instance.Id, instance.ProjectId, instance.Location, nil
}

func findInstance(ctx context.Context,
	client *crusoeapi.APIClient, instanceName string,
) (*crusoeapi.InstanceV1Alpha5, error) {
	opts := &crusoeapi.ProjectsApiListProjectsOpts{
		OrgId: optional.EmptyString(),
	}

	projectsResp, projectHTTPResp, err := client.ProjectsApi.ListProjects(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to query for projects: %w", err)
	}

	defer projectHTTPResp.Body.Close()

	for _, project := range projectsResp.Items {
		listVMOpts := &crusoeapi.VMsApiListInstancesOpts{
			Names: optional.NewString(instanceName),
		}
		instances, instancesHTTPResp, instancesErr := client.VMsApi.ListInstances(ctx, project.Id, listVMOpts)
		if instancesErr != nil {
			return nil, fmt.Errorf("failed to list instances: %w", instancesErr)
		}
		instancesHTTPResp.Body.Close()

		if len(instances.Items) == 0 {
			continue
		}

		for i := range instances.Items {
			if instances.Items[i].Name == instanceName {
				return &instances.Items[i], nil
			}
		}
	}

	return nil, errInstanceNotFound
}

func ReadEnvVar(secretName string) string {
	return os.Getenv(secretName)
}

func GetNodeFQDN() string {
	return ReadEnvVar("NODE_NAME")
}
