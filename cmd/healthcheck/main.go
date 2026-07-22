package main
import("net/http";"os")
func main(){addr:=os.Getenv("HEALTHCHECK_ADDR");if addr==""{addr="http://127.0.0.1:3000/healthz"};r,e:=http.Get(addr);if e!=nil||r.StatusCode!=200{os.Exit(1)}}
