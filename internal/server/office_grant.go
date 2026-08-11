package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

const officeExecutionGrantVersion = "ecs.office.execution-grant/v1"

type officeExecutionGrantPayload struct {
	Version string                     `json:"version"`
	Issuer  string                     `json:"issuer"`
	Subject string                     `json:"subject"`
	Request officeTaskExecutionRequest `json:"request"`
}

func signOfficeExecutionGrant(
	secret, issuer, subject string,
	request officeTaskExecutionRequest,
) (*officeExecutionGrant, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("Office execution grant requires a peer credential")
	}
	grant := &officeExecutionGrant{
		Version: officeExecutionGrantVersion,
		Issuer:  strings.TrimSpace(issuer), Subject: strings.TrimSpace(subject),
	}
	payload, err := officeGrantPayload(*grant, request)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	grant.Signature = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return grant, nil
}

func verifyOfficeExecutionGrant(
	secret, expectedIssuer, localNodeID, localNodeName string,
	request officeTaskExecutionRequest,
) error {
	grant := request.ApprovalGrant
	if grant == nil || grant.Version != officeExecutionGrantVersion ||
		strings.TrimSpace(grant.Signature) == "" {
		return errors.New("signed Office execution grant is required")
	}
	if grant.Issuer != expectedIssuer {
		return errors.New("Office execution grant issuer does not match the authenticated controller")
	}
	if grant.Subject != localNodeID && grant.Subject != localNodeName {
		return errors.New("Office execution grant is not addressed to this worker")
	}
	payload, err := officeGrantPayload(*grant, request)
	if err != nil {
		return err
	}
	actual, err := base64.RawURLEncoding.DecodeString(grant.Signature)
	if err != nil {
		return errors.New("Office execution grant signature is invalid")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	if !hmac.Equal(actual, mac.Sum(nil)) {
		return errors.New("Office execution grant does not match the task request")
	}
	return nil
}

func officeGrantPayload(
	grant officeExecutionGrant,
	request officeTaskExecutionRequest,
) ([]byte, error) {
	grant.Signature = ""
	request.ApprovalGrant = nil
	return json.Marshal(officeExecutionGrantPayload{
		Version: grant.Version, Issuer: grant.Issuer, Subject: grant.Subject, Request: request,
	})
}
