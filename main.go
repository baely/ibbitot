package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/baely/balance/pkg/model"
	office "github.com/baely/officetracker/pkg/model"
)

const (
	projectID  = "inoffice-23952"
	collection = "baileypresent"
	document   = "transaction"
)

var (
	//go:embed index.html
	indexHTML string

	//go:embed coffee-cup.png
	coffeeCup []byte
)

var (
	officeTrackerAPIKey = os.Getenv("OFFICETRACKER_API_KEY")
	loc, _              = time.LoadLocation("Australia/Melbourne")
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	m := http.NewServeMux()
	m.HandleFunc("/webhook", webhookHandler)
	m.HandleFunc("/raw", rawHandler)
	m.HandleFunc("/", indexHandler)
	m.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "image/png")
		w.Header().Add("Cache-Control", "public, max-age=604800, immutable")
		w.Write(coffeeCup)
	})
	s := &http.Server{
		Addr:    ":" + port,
		Handler: m,
	}
	fmt.Printf("listening on port %s\n", port)
	if err := s.ListenAndServe(); err != nil {
		panic(err)
	}
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	latestTransaction, err := getLatest()
	if err != nil {
		slog.Error("Error getting latest transaction: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	m := model.RawWebhookEvent{}
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		slog.Error("Error decoding webhook event: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if err := updatePresence(latestTransaction, m.Transaction); err != nil {
		slog.Error("Error updating presence: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

func rawHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	latestTransaction, err := getLatest()
	if err != nil {
		slog.Error("Error getting latest transaction: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "text/plain")
	w.Write([]byte(presentString(latestTransaction)))
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	latestTransaction, err := getLatest()
	if err != nil {
		slog.Error("Error getting latest transaction: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	tmpl := template.Must(template.New("index").Parse(indexHTML))
	err = tmpl.Execute(w, struct {
		Presence    string
		Transaction struct {
			Description string
			Amount      string
			Time        string
		}
		Reason string
	}{
		Presence: presentString(latestTransaction),
		Transaction: struct {
			Description string
			Amount      string
			Time        string
		}{
			Description: latestTransaction.Attributes.Description,
			Amount:      fmt.Sprintf("$%.2f", -float64(latestTransaction.Attributes.Amount.ValueInBaseUnits)/100.0),
			Time:        latestTransaction.Attributes.CreatedAt.Format(time.RFC1123),
		},
		Reason: getReason(present(latestTransaction)),
	})
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func updatePresence(prevTransaction, transaction model.TransactionResource) error {
	if !check(transaction,
		amountBetween(-700, -400), // between -$7 and -$4
		//timeBetween(6, 12),                // between 6am and 12pm
		weekday(),                         // on a weekday
		notForeign(),                      // not a foreign transaction
		category("restaurants-and-cafes"), // in the restaurants-and-cafes category
	) {
		slog.Warn("Transaction does not meet criteria")
		return nil
	}

	return store(prevTransaction, transaction)
}

func present(latestTransaction model.TransactionResource) bool {
	return check(latestTransaction,
		fresh(),
	)
}

func presentString(latestTransaction model.TransactionResource) string {
	if present(latestTransaction) {
		return "yes"
	}
	return "no"
}

type decider func(model.TransactionResource) bool

func check(transaction model.TransactionResource, deciders ...decider) bool {
	for _, d := range deciders {
		if !d(transaction) {
			return false
		}
	}
	return true
}

func amountBetween(minBaseUnits, maxBaseUnits int) decider {
	return func(transaction model.TransactionResource) bool {
		valueInBaseUnits := transaction.Attributes.Amount.ValueInBaseUnits
		return valueInBaseUnits >= minBaseUnits && valueInBaseUnits <= maxBaseUnits
	}
}

func timeBetween(minHour, maxHour int) decider {
	return func(transaction model.TransactionResource) bool {
		hour := transaction.Attributes.CreatedAt.Hour()
		return hour >= minHour && hour <= maxHour
	}
}

func weekday() decider {
	return func(transaction model.TransactionResource) bool {
		day := transaction.Attributes.CreatedAt.Weekday()
		return day >= 1 && day <= 5
	}
}

func fresh() decider {
	return func(transaction model.TransactionResource) bool {
		return isToday(transaction)
	}
}

func notForeign() decider {
	return func(transaction model.TransactionResource) bool {
		return transaction.Attributes.ForeignAmount == nil
	}
}

func category(categoryId string) decider {
	return func(transaction model.TransactionResource) bool {
		return transaction.Relationships.Category.Data.Id == categoryId
	}
}

func isToday(transaction model.TransactionResource) bool {
	now := time.Now().In(loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return transaction.Attributes.CreatedAt.After(midnight)
}

func getLatest() (model.TransactionResource, error) {
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return model.TransactionResource{}, err
	}
	doc := client.Collection(collection).Doc(document)
	ref, err := doc.Get(ctx)
	if err != nil {
		return model.TransactionResource{}, err
	}
	var transaction model.TransactionResource
	err = ref.DataTo(&transaction)
	if err != nil {
		return model.TransactionResource{}, err
	}
	return transaction, nil
}

func must[T any](t T, err error) T {
	if err != nil {
		panic(err)
	}
	return t
}

func getReason(presence bool) string {
	state := getOfficeStatus()
	if !presence && state == office.StateWorkFromOffice {
		return "(but he said he would be)"
	}
	return ""
}

func getOfficeStatus() office.State {
	uriPattern := "https://officetracker.baileys.page/api/v1/state/%d/%d/%d"
	now := time.Now().In(loc)
	uriStr := fmt.Sprintf(uriPattern, now.Year(), now.Month(), now.Day())
	uri := must(url.Parse(uriStr))
	req := &http.Request{
		Method: http.MethodGet,
		URL:    uri,
		Header: map[string][]string{
			"Authorization": {"Bearer " + officeTrackerAPIKey},
		},
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("Error getting office status: %v", err)
		return office.StateUntracked
	}
	defer resp.Body.Close()
	var stateResp office.GetDayResponse
	if err := json.NewDecoder(resp.Body).Decode(&stateResp); err != nil {
		slog.Error("Error decoding office status: %v", err)
		return office.StateUntracked
	}
	return stateResp.Data.State
}

func updateOfficeStatus() error {
	existingStatus := getOfficeStatus()
	if existingStatus != office.StateUntracked {
		return nil
	}
	uriPattern := "https://officetracker.baileys.page/api/v1/state/%d/%d/%d"
	now := time.Now().In(loc)
	uriStr := fmt.Sprintf(uriPattern, now.Year(), now.Month(), now.Day())
	uri := must(url.Parse(uriStr))
	stateReq := office.PutDayRequest{
		Data: office.DayState{
			State: office.StateWorkFromOffice,
		},
	}
	b, err := json.Marshal(stateReq)
	if err != nil {
		return err
	}
	req := &http.Request{
		Method: http.MethodPut,
		URL:    uri,
		Header: map[string][]string{
			"Authorization": {"Bearer " + officeTrackerAPIKey},
		},
		Body: io.NopCloser(bytes.NewReader(b)),
	}
	_, err = http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return nil
}

func store(prevTransaction, transaction model.TransactionResource) error {
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return err
	}
	doc := client.Collection(collection).Doc(document)
	if prevTransaction.Attributes.CreatedAt.After(transaction.Attributes.CreatedAt) {
		return nil
	}
	_, err = doc.Set(ctx, transaction)
	if err != nil {
		return err
	}
	err = updateOfficeStatus()
	if err != nil {
		return err
	}
	return nil
}
