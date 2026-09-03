package appstore

import (
	"fmt"
	gohttp "net/http"
	"net/url"
	"strings"

	"github.com/majd/ipatool/v2/pkg/http"
)

const (
	downloadVersionKeyVolumeStore = "externalVersionId"
	downloadVersionKeyRedownload  = "appExtVrsId"
)

type downloadProductEndpoint struct {
	baseURL    string
	versionKey string
}

func (t *appstore) sendDownloadProduct(acc Account, app App, guid, externalVersionID string) (http.Result[downloadResult], error) {
	volumeStore := t.volumeStoreEndpoint(acc)

	res, err := t.downloadClient.Send(t.downloadProductRequest(volumeStore, acc, app, guid, externalVersionID))
	if err != nil {
		return res, fmt.Errorf("failed to send http request: %w", err)
	}

	if !isEmptyDownloadProductResponse(res) {
		return res, nil
	}

	// Some owned apps return HTTP 200 with no items or failure metadata from
	// volumeStore. In that exact case, redownloadProduct can still serve the app.
	bag, err := t.bag(guid)
	if err != nil {
		return res, fmt.Errorf("failed to get bag for redownload fallback: %w", err)
	}

	if bag.RedownloadEndpoint == "" {
		return res, nil
	}

	redownload, err := newRedownloadEndpoint(bag.RedownloadEndpoint)
	if err != nil {
		return res, err
	}

	redownloadRes, err := t.downloadClient.Send(t.downloadProductRequest(redownload, acc, app, guid, externalVersionID))
	if err != nil {
		return redownloadRes, fmt.Errorf("failed to send redownload request: %w", err)
	}

	return redownloadRes, nil
}

func isEmptyDownloadProductResponse(res http.Result[downloadResult]) bool {
	return res.StatusCode == gohttp.StatusOK &&
		res.Data.FailureType == "" &&
		res.Data.CustomerMessage == "" &&
		len(res.Data.Items) == 0
}

func (*appstore) volumeStoreEndpoint(acc Account) downloadProductEndpoint {
	podPrefix := ""
	if acc.Pod != "" {
		podPrefix = "p" + acc.Pod + "-"
	}

	return downloadProductEndpoint{
		baseURL:    fmt.Sprintf("https://%s%s%s", podPrefix, PrivateAppStoreAPIDomain, PrivateAppStoreAPIPathDownload),
		versionKey: downloadVersionKeyVolumeStore,
	}
}

func newRedownloadEndpoint(endpoint string) (downloadProductEndpoint, error) {
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return downloadProductEndpoint{}, fmt.Errorf("invalid redownload endpoint %q in bag", endpoint)
	}

	if strings.ToLower(parsed.Hostname()) != PrivateAppStoreRedownloadDomain || parsed.Path != PrivateAppStoreRedownloadPath {
		return downloadProductEndpoint{}, fmt.Errorf("unsupported redownload endpoint %q in bag", endpoint)
	}

	return downloadProductEndpoint{
		baseURL:    endpoint,
		versionKey: downloadVersionKeyRedownload,
	}, nil
}

func (*appstore) downloadProductRequest(endpoint downloadProductEndpoint, acc Account, app App, guid, externalVersionID string) http.Request {
	payload := map[string]interface{}{
		"creditDisplay": "",
		"guid":          guid,
		"salableAdamId": app.ID,
		"serialNumber":  "0",
	}

	if externalVersionID != "" {
		payload[endpoint.versionKey] = externalVersionID
	}

	return http.Request{
		URL:            fmt.Sprintf("%s?guid=%s", endpoint.baseURL, guid),
		Method:         http.MethodPOST,
		ResponseFormat: http.ResponseFormatXML,
		Headers: map[string]string{
			"Content-Type": "application/x-apple-plist",
			"iCloud-DSID":  acc.DirectoryServicesID,
			"X-Dsid":       acc.DirectoryServicesID,
		},
		Payload: &http.XMLPayload{
			Content: payload,
		},
	}
}
