package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	tokenMetaKey    = "paypal-token"
	tokenRefreshing = 5 * time.Minute // a cached token is renewed this long before it ends
	maxPayPalHeader = 2048
)

// paypalBase is the API host of the configured mode.
func paypalBase(s settings) string {
	if s.PayPalSandbox {
		return paypalSandAPI
	}
	return paypalLiveAPI
}

// paypalCall runs one PayPal API request. A body is sent as JSON. Every
// status outside 2xx is an error without the answer's text.
func paypalCall(s settings, method, path, token, reqID string, body any, dl deadline, out any) error {
	headers := map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"}
	raw := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return errProviderRefused
		}
		raw = string(b)
		headers["Content-Type"] = "application/json"
	}
	if reqID != "" {
		headers["PayPal-Request-Id"] = reqID
	}
	return paypalDo(method, paypalBase(s)+path, headers, raw, dl, out)
}

func paypalDo(method, rawURL string, headers map[string]string, body string, dl deadline, out any) error {
	status, answer, err := fetchJSON(method, rawURL, headers, body, dl)
	if err != nil {
		return err
	}
	switch {
	case status >= 500 || status == 429:
		return errProviderDown
	case status < 200 || status > 299:
		return errProviderRefused
	}
	if out != nil && json.Unmarshal(answer, out) != nil {
		return errProviderBadAnswer
	}
	return nil
}

// cachedToken is the access token kept in meta. H ties it to the client id and
// mode it was issued for, so changing either settings drops it.
type cachedToken struct {
	H   string `json:"h"`
	T   string `json:"t"`
	Exp int64  `json:"exp"`
}

func tokenBinding(s settings) string {
	sum := sha256.Sum256([]byte(paypalBase(s) + "|" + s.PayPalClientID))
	return hex.EncodeToString(sum[:])[:16]
}

// paypalToken is a valid access token: the cached one, else a new one.
func paypalToken(s settings, dl deadline) (string, error) {
	if !s.paypalReady() {
		return "", errProviderRefused
	}
	h := tokenBinding(s)
	var c cachedToken
	if err := dbJSON(`SELECT coalesce((SELECT to_jsonb(value) FROM meta WHERE key = $1), 'null'::jsonb)`, &c, tokenMetaKey); err == nil &&
		c.H == h && c.T != "" && time.Unix(c.Exp, 0).After(now().Add(tokenRefreshing)) {
		return c.T, nil
	}
	headers := map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(s.PayPalClientID+":"+s.PayPalSecret)),
		"Accept":        "application/json",
		"Content-Type":  "application/x-www-form-urlencoded",
	}
	var res struct {
		Token   string `json:"access_token"`
		Expires int64  `json:"expires_in"`
	}
	if err := paypalDo("POST", paypalBase(s)+"/v1/oauth2/token", headers, "grant_type=client_credentials", dl, &res); err != nil {
		return "", err
	}
	if res.Token == "" || res.Expires < 1 {
		return "", errProviderBadAnswer
	}
	b, err := json.Marshal(cachedToken{H: h, T: res.Token, Exp: now().Unix() + res.Expires})
	if err == nil { // a failed write only costs the next call a new token
		_ = dbExec(`INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, tokenMetaKey, string(b))
	}
	return res.Token, nil
}

// paypalCreateOrder creates a PayPal order for a payIn and returns its id and
// the page to send the buyer to. custom_id carries the pay code, which the
// capture must echo back.
func paypalCreateOrder(s settings, o payIn, dl deadline) (id, approveURL string, err error) {
	token, err := paypalToken(s, dl)
	if err != nil {
		return "", "", err
	}
	body := map[string]any{
		"intent": "CAPTURE",
		"purchase_units": []any{map[string]any{
			"custom_id":   o.PayCode,
			"description": o.Title,
			"amount":      map[string]string{"currency_code": strings.ToUpper(o.Currency), "value": decimalAmount(o.Total, o.Currency)},
		}},
		"payment_source": map[string]any{"paypal": map[string]any{"experience_context": map[string]string{
			"return_url": o.SuccessURL, "cancel_url": o.CancelURL, "user_action": "PAY_NOW", "shipping_preference": "NO_SHIPPING",
		}}},
	}
	var res struct {
		ID    string `json:"id"`
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if err := paypalCall(s, "POST", "/v2/checkout/orders", token, "et-"+strconv.FormatInt(o.OrderID, 10)+"-create", body, dl, &res); err != nil {
		return "", "", err
	}
	if !idRe.MatchString(res.ID) {
		return "", "", errProviderBadAnswer
	}
	for _, rel := range []string{"payer-action", "approve"} {
		for _, l := range res.Links {
			if l.Rel == rel && l.Href != "" {
				return res.ID, l.Href, nil
			}
		}
	}
	return "", "", errProviderBadAnswer
}

// paypalCaptured is what a capture answered, flattened to what is checked.
type paypalCaptured struct {
	Status        string // of the order
	CaptureID     string
	CaptureStatus string
	Value         string // decimal string
	Currency      string
	CustomID      string
}

// paypalCaptureOrder captures an approved order. The request id is the same
// for every capture of an order, so a repeat returns the first answer.
func paypalCaptureOrder(s settings, orderRef, reqID string, dl deadline) (paypalCaptured, error) {
	var out paypalCaptured
	if !idRe.MatchString(orderRef) {
		return out, errProviderRefused
	}
	token, err := paypalToken(s, dl)
	if err != nil {
		return out, err
	}
	var res struct {
		Status        string `json:"status"`
		PurchaseUnits []struct {
			CustomID string `json:"custom_id"`
			Payments struct {
				Captures []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Amount struct {
						Value    string `json:"value"`
						Currency string `json:"currency_code"`
					} `json:"amount"`
					CustomID string `json:"custom_id"`
				} `json:"captures"`
			} `json:"payments"`
		} `json:"purchase_units"`
	}
	if err := paypalCall(s, "POST", "/v2/checkout/orders/"+orderRef+"/capture", token, reqID, struct{}{}, dl, &res); err != nil {
		return out, err
	}
	out.Status = res.Status
	if len(res.PurchaseUnits) == 0 || len(res.PurchaseUnits[0].Payments.Captures) == 0 {
		return out, nil
	}
	pu := res.PurchaseUnits[0]
	c := pu.Payments.Captures[0]
	out.CaptureID, out.CaptureStatus, out.Value, out.Currency = c.ID, c.Status, c.Amount.Value, c.Amount.Currency
	out.CustomID = c.CustomID
	if out.CustomID == "" {
		out.CustomID = pu.CustomID
	}
	return out, nil
}

// paypalHeaders are the five transmission headers of a webhook.
type paypalHeaders struct {
	TransmissionID, TransmissionTime, TransmissionSig, CertURL, AuthAlgo string
}

func (h paypalHeaders) complete() bool {
	for _, v := range []string{h.TransmissionID, h.TransmissionTime, h.TransmissionSig, h.CertURL, h.AuthAlgo} {
		if v == "" || len(v) > maxPayPalHeader {
			return false
		}
	}
	return true
}

// paypalVerifyWebhook asks PayPal whether a webhook is genuine. The posted
// body goes in unchanged as webhook_event; only a SUCCESS answer is true.
func paypalVerifyWebhook(s settings, h paypalHeaders, body string, dl deadline) (bool, error) {
	if s.PayPalWebhookID == "" || !h.complete() || !json.Valid([]byte(body)) {
		return false, nil
	}
	token, err := paypalToken(s, dl)
	if err != nil {
		return false, err
	}
	// json.Marshal would compact a RawMessage; the signature covers the bytes
	// PayPal sent, so the body is spliced in as it came.
	head, err := json.Marshal(map[string]string{
		"transmission_id": h.TransmissionID, "transmission_time": h.TransmissionTime, "transmission_sig": h.TransmissionSig,
		"cert_url": h.CertURL, "auth_algo": h.AuthAlgo, "webhook_id": s.PayPalWebhookID,
	})
	if err != nil {
		return false, errProviderRefused
	}
	req := string(head[:len(head)-1]) + `,"webhook_event":` + body + "}"
	headers := map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json", "Content-Type": "application/json"}
	var res struct {
		Status string `json:"verification_status"`
	}
	if err := paypalDo("POST", paypalBase(s)+"/v1/notifications/verify-webhook-signature", headers, req, dl, &res); err != nil {
		return false, err
	}
	return res.Status == "SUCCESS", nil
}

// paypalRefund refunds amount of a capture and returns the refund id.
func paypalRefund(s settings, captureID string, amount int64, cur, reqID string, dl deadline) (string, error) {
	if !idRe.MatchString(captureID) || amount < 1 || amount > maxTotalCents {
		return "", errProviderRefused
	}
	token, err := paypalToken(s, dl)
	if err != nil {
		return "", err
	}
	body := map[string]any{"amount": map[string]string{"currency_code": strings.ToUpper(cur), "value": decimalAmount(amount, cur)}}
	var res struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := paypalCall(s, "POST", "/v2/payments/captures/"+captureID+"/refund", token, reqID, body, dl, &res); err != nil {
		return "", err
	}
	if !idRe.MatchString(res.ID) || (res.Status != "COMPLETED" && res.Status != "PENDING") {
		return "", errProviderBadAnswer
	}
	return res.ID, nil
}
