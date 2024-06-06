package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/baely/balance/pkg/model"
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
	latestTransaction model.TransactionResource
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
	refreshTransaction()
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m := model.RawWebhookEvent{}
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		slog.Error("Error decoding webhook event: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if err := updatePresence(m.Transaction); err != nil {
		slog.Error("Error updating presence: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

func refreshTransaction() {
	latestTransaction, _ = getLatest()
}

func rawHandler(w http.ResponseWriter, r *http.Request) {
	refreshTransaction()
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Add("Content-Type", "text/plain")
	w.Write([]byte(presentString()))
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	refreshTransaction()
	tmpl := template.Must(template.New("index").Parse(indexHTML))
	err := tmpl.Execute(w, struct {
		Presence    string
		Transaction struct {
			Description string
			Amount      string
			Time        string
		}
	}{
		Presence: presentString(),
		Transaction: struct {
			Description string
			Amount      string
			Time        string
		}{
			Description: latestTransaction.Attributes.Description,
			Amount:      fmt.Sprintf("$%.2f", -float64(latestTransaction.Attributes.Amount.ValueInBaseUnits)/100.0),
			Time:        latestTransaction.Attributes.CreatedAt.Format(time.RFC1123),
		},
	})
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func updatePresence(transaction model.TransactionResource) error {
	if !check(transaction,
		amountBetween(-700, -400),         // between -$7 and -$4
		timeBetween(6, 12),                // between 6am and 12pm
		weekday(),                         // on a weekday
		notForeign(),                      // not a foreign transaction
		category("restaurants-and-cafes"), // in the restaurants-and-cafes category
	) {
		slog.Warn("Transaction does not meet criteria")
		return nil
	}

	return store(transaction)
}

func present() bool {
	return check(latestTransaction,
		fresh(12*time.Hour),
	)
}

func presentString() string {
	if present() {
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

func fresh(maxAge time.Duration) decider {
	return func(transaction model.TransactionResource) bool {
		age := time.Since(transaction.Attributes.CreatedAt)
		return age <= maxAge
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

func store(transaction model.TransactionResource) error {
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return err
	}
	doc := client.Collection(collection).Doc(document)
	if latestTransaction.Attributes.CreatedAt.After(transaction.Attributes.CreatedAt) {
		return nil
	}
	_, err = doc.Set(ctx, transaction)
	if err != nil {
		return err
	}
	return nil
}
