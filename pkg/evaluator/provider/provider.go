// Package provider constructs independently configured evaluator clients.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/environment"
	"github.com/docker/docker-agent/pkg/evaluator"
	"github.com/docker/docker-agent/pkg/httpclient"
)

const (
	defaultBaseURL = "https://api.typesafe.ai"
	defaultTimeout = 10 * time.Second
)

// New builds a reusable client from a resolved evaluator configuration.
// Credentials are obtained from env on each evaluation, not during construction.
func New(ctx context.Context, cfg latest.EvaluatorConfig, env environment.Provider) (evaluator.Evaluator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, errors.New("invalid evaluator configuration")
	}
	if cfg.Provider != "typesafe" {
		return nil, errors.New("unsupported evaluator provider")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("evaluator model is required")
	}
	if env == nil {
		return nil, errors.New("evaluator environment provider is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("evaluator base URL must be an HTTP(S) URL without credentials, query, or fragment")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = strings.TrimRight(baseURL, "/") + "/v1/systemone"
	}

	timeout := cfg.Timeout.Duration
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if cfg.TokenKey == "" {
		cfg.TokenKey = "TYPESAFE_API_KEY"
	}

	question := typesafeQuestion{Type: cfg.Type, Instructions: cfg.Instructions}
	var probabilityKeys []string
	switch cfg.Type {
	case "boolean":
		question.Type = "noul"
	case "choice":
		question.Criteria = cfg.Choices
		probabilityKeys = slices.Sorted(maps.Keys(cfg.Choices))
	case "score":
		question.Criteria = cfg.Levels
		for i := range cfg.Levels {
			probabilityKeys = append(probabilityKeys, strconv.Itoa(i))
		}
	}
	questionJSON, err := json.Marshal(question)
	if err != nil {
		return nil, errors.New("invalid evaluator question")
	}

	var cost *latest.CostConfig
	if cfg.Cost != nil {
		price := *cfg.Cost
		cost = &price
	}
	client := httpclient.NewHTTPClient(ctx)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &typesafe{
		client:          client,
		env:             env,
		endpoint:        endpoint,
		tokenKey:        cfg.TokenKey,
		model:           cfg.Model,
		resultType:      cfg.Type,
		questionType:    question.Type,
		question:        questionJSON,
		probabilityKeys: probabilityKeys,
		timeout:         timeout,
		cost:            cost,
		officialPricing: endpoint == defaultBaseURL+"/v1/systemone",
	}, nil
}
