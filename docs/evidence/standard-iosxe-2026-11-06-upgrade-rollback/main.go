package main
import (
 "encoding/json"
 "log"
 "net/http"
)
var release = "UNSET"
func main() {
 http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Content-Type", "application/json")
  json.NewEncoder(w).Encode(map[string]string{"fixture":"cvk-upgrade-rollback-qualification","release":release})
 })
 log.Fatal(http.ListenAndServe(":8080", nil))
}
