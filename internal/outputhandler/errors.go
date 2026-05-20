package outputhandler

import (
	"encoding/json"
	"net/http"
)

// WriteHTTPError writes a JSON error response to the client.
func WriteHTTPError(w http.ResponseWriter, err error) {
	errorMsg := map[string]string{
		"error": err.Error(),
	}
	errorMsgDat, jsonErr := json.Marshal(errorMsg)
	if jsonErr == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(errorMsgDat)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
