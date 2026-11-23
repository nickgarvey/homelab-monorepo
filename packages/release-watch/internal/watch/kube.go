package watch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Kube lists pods through the Kubernetes API with the pod's ServiceAccount
// token, read again for every request since the kubelet rotates it.
type Kube struct {
	Client    *http.Client
	BaseURL   string
	TokenFile string
}

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// InClusterKube is Kube for the cluster this pod runs in.
func InClusterKube() (*Kube, error) {
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("%s/ca.crt: no certificates", saDir)
	}
	return &Kube{
		Client: &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}},
		BaseURL:   "https://kubernetes.default.svc",
		TokenFile: saDir + "/token",
	}, nil
}

type podList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Containers []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
			} `json:"containers"`
		} `json:"spec"`
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	} `json:"items"`
}

// RunningImages is the image of container in each running pod in namespace
// that matches the label selector.
func (k *Kube) RunningImages(ctx context.Context, namespace, selector, container string) ([]string, error) {
	token, err := os.ReadFile(k.TokenFile)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/api/v1/namespaces/%s/pods?labelSelector=%s",
		k.BaseURL, url.PathEscape(namespace), url.QueryEscape(selector))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Accept", "application/json")
	resp, err := k.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("list pods in %s: %s: %s", namespace, resp.Status, body)
	}
	var pl podList
	if err := json.NewDecoder(resp.Body).Decode(&pl); err != nil {
		return nil, fmt.Errorf("list pods in %s: %w", namespace, err)
	}
	var images []string
	for _, p := range pl.Items {
		if p.Status.Phase != "Running" {
			continue
		}
		for _, c := range p.Spec.Containers {
			if c.Name == container {
				images = append(images, c.Image)
			}
		}
	}
	return images, nil
}
