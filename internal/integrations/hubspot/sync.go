package hubspotintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const companiesURL = "https://api.hubapi.com/crm/v3/objects/companies"

func (s *Service) checkAccess(ctx context.Context, token string) error {
	request, err := companyRequest(ctx, token, "", 1)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HubSpot returned status %d", response.StatusCode)
	}
	return nil
}

func (s *Service) listCompanies(ctx context.Context, token string) ([]companyRecord, error) {
	var companies []companyRecord
	after := ""
	for {
		request, err := companyRequest(ctx, token, after, 100)
		if err != nil {
			return nil, err
		}
		response, err := s.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("request HubSpot companies: %w", err)
		}
		var page struct {
			Results []struct {
				ID         string            `json:"id"`
				Properties map[string]string `json:"properties"`
				UpdatedAt  time.Time         `json:"updatedAt"`
			} `json:"results"`
			Paging struct {
				Next *struct {
					After string `json:"after"`
				} `json:"next"`
			} `json:"paging"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("HubSpot returned status %d", response.StatusCode)
		}
		if decodeErr != nil {
			return nil, decodeErr
		}
		for _, item := range page.Results {
			observedAt := item.UpdatedAt.UTC()
			if observedAt.IsZero() {
				observedAt = time.Now().UTC()
			}
			companies = append(companies, companyRecord{
				ID: item.ID, Name: strings.TrimSpace(item.Properties["name"]),
				Domain: normalizeDomain(item.Properties["domain"]),
				Status: item.Properties["lifecyclestage"], ObservedAt: observedAt,
			})
		}
		if page.Paging.Next == nil || page.Paging.Next.After == "" {
			return companies, nil
		}
		after = page.Paging.Next.After
	}
}

func companyRequest(ctx context.Context, token, after string, limit int) (*http.Request, error) {
	requestURL, _ := url.Parse(companiesURL)
	query := requestURL.Query()
	query.Set("limit", fmt.Sprint(limit))
	query.Set("properties", "name,domain,lifecyclestage")
	if after != "" {
		query.Set("after", after)
	}
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err == nil {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request, err
}

func normalizeStatus(value string) string {
	if value == "" {
		return "unknown"
	}
	if strings.EqualFold(value, "customer") {
		return "active"
	}
	return "inactive"
}

func normalizeDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(strings.TrimPrefix(value, "http://"), "https://")
	value = strings.TrimPrefix(value, "www.")
	if index := strings.IndexAny(value, "/:?#"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSuffix(value, ".")
}
