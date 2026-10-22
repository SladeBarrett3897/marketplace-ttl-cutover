package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/infrai-examples/marketplace-ttl-cutover/internal/cutover"
)

type server struct {
	workflow *cutover.Workflow
}

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = ":8080"
	}

	dns := cutover.NewDNSClient(apiKey, nil)
	s := &server{workflow: cutover.NewWorkflow(dns)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /cutovers", s.createCutover)
	mux.HandleFunc("POST /cutovers/{cutoverID}/rollback", s.rollback)
	log.Printf("marketplace cutover service listening on %s", address)
	log.Fatal(http.ListenAndServe(address, mux))
}

func (s *server) createCutover(w http.ResponseWriter, r *http.Request) {
	var input cutover.Request
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.workflow.CutOver(r.Context(), input)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) rollback(w http.ResponseWriter, r *http.Request) {
	result, err := s.workflow.RollBack(r.Context(), r.PathValue("cutoverID"))
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeWorkflowError(w http.ResponseWriter, err error) {
	var apiErr *cutover.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		writeJSON(w, apiErr.HTTPStatus, map[string]string{"error": apiErr.Code, "message": apiErr.Message})
		return
	}
	if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "ttl must") || strings.Contains(err.Error(), "not found") {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "DNS update was not completed")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}
