package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"

	emailVerifier "github.com/AfterShip/email-verifier"
)

var verifier = emailVerifier.
	NewVerifier().
	EnableSMTPCheck().
	EnableDomainSuggest().
	FromEmail("hello@syncoretech.com").
	HelloName("syncoretech.com")

func GetEmailVerification(
	w http.ResponseWriter,
	r *http.Request,
	ps httprouter.Params,
) {
	ret, err := verifier.Verify(ps.ByName("email"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !ret.Syntax.Valid {
		http.Error(w, "email address syntax is invalid", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	response, err := json.Marshal(ret)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, _ = fmt.Fprint(w, string(response))
}

func main() {
	router := httprouter.New()

	router.GET("/v1/:email/verification", GetEmailVerification)

	server := &http.Server{
		Addr:         "127.0.0.1:8080",
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	log.Println("Email verifier running at http://127.0.0.1:8080")
	log.Fatal(server.ListenAndServe())
}
