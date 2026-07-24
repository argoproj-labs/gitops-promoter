package azuredevops

func AzureDevOpsHTTPStatusCode(err error) (int, bool) {
	return azureDevOpsHTTPStatusCode(err)
}

func IsAzureDevOpsNotFound(err error) bool {
	return isAzureDevOpsNotFound(err)
}
