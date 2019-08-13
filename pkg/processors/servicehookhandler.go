package processors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alexcesaro/log/stdlog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/ggmaresca/azd-kubernetes-manager/pkg/args"
	"github.com/ggmaresca/azd-kubernetes-manager/pkg/azuredevops"
	"github.com/ggmaresca/azd-kubernetes-manager/pkg/config"
	"github.com/ggmaresca/azd-kubernetes-manager/pkg/kubernetes"
)

var (
	logger = stdlog.GetFromFlags()

	serviceHookCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "azd_kubernetes_manager_service_hook_count",
		Help: "The total number of Service Hooks",
	}, []string{"eventType"})

	serviceHookDurationHistogram = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "azd_kubernetes_manager_service_hook_duration_seconds",
		Help: "The duration of Service Hook requests",
	}, []string{"eventType"})

	serviceHookErrorCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "azd_kubernetes_manager_service_hook_error_count",
		Help: "The total number of Service Hooks",
	}, []string{"eventType", "reason"})
)

// ServiceHookHandler is an HTTP handler for service hooks
type ServiceHookHandler struct {
	args      args.Args
	config    config.File
	k8sClient kubernetes.ClientAsync
}

func (h ServiceHookHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()

	// Assert HTTP method
	if !strings.EqualFold(request.Method, "POST") {
		logger.Errorf("Service hooks must be POST requests - received %s method", request.Method)
		serviceHookCounter.With(prometheus.Labels{"eventType": "unknown"}).Inc()
		serviceHookErrorCounter.With(prometheus.Labels{"eventType": "unknown", "reason": fmt.Sprintf("HTTP %d Method Not Allowed", http.StatusMethodNotAllowed)}).Inc()
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Read body into string
	buffer := new(bytes.Buffer)
	_, err := buffer.ReadFrom(request.Body)
	if err != nil {
		logger.Errorf("Error reading request body from service hook: %s", err.Error())
		serviceHookCounter.With(prometheus.Labels{"eventType": "unknown"}).Inc()
		serviceHookErrorCounter.With(prometheus.Labels{"eventType": "unknown", "reason": "Error reading body"}).Inc()
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	requestStr := string(buffer.Bytes())
	if logger.LogDebug() {
		logger.Debugf("Received service hook: %s", requestStr)
	}

	// Parse JSON
	requestObj := new(azuredevops.ServiceHook)
	if err = json.NewDecoder(strings.NewReader(requestStr)).Decode(requestObj); err != nil {
		logger.Errorf("Error - could not parse JSON from Service hook. Error: %s\nRequest: %s", err.Error(), requestStr)
		serviceHookCounter.With(prometheus.Labels{"eventType": "unknown"}).Inc()
		serviceHookErrorCounter.With(prometheus.Labels{"eventType": "unknown", "reason": "JSON parse error"}).Inc()
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// Prometheus metrics
	serviceHookCounter.With(prometheus.Labels{"eventType": requestObj.EventType}).Inc()
	defer prometheus.NewTimer(serviceHookDurationHistogram.With(prometheus.Labels{"eventType": requestObj.EventType})).ObserveDuration()

	if logger.LogDebug() {
		logger.Debugf("Deserialized response to: %#v", requestObj)
	}

	// Validate basic authentication
	username, password, ok := request.BasicAuth()
	if h.args.ServiceHooks.UseBasicAuthentication() {
		if ok && username == h.args.ServiceHooks.Username && password == h.args.ServiceHooks.Password {
			if logger.LogDebug() {
				logger.Debugf("Validated basic authentication for request \"%s\"", requestObj.Describe())
			}
		} else {
			logger.Errorf("Failed to validate basic authentication for request \"%s\"")
			serviceHookErrorCounter.With(prometheus.Labels{"eventType": requestObj.EventType, "reason": fmt.Sprintf("HTTP %d Unauthorized", http.StatusUnauthorized)}).Inc()
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
	} else if ok {
		logger.Infof("Basic authentication was provided in request \"%s\", but basic authentication was not configured.", requestObj.Describe())
	}

	writer.WriteHeader(http.StatusOK)
	writer.Write([]byte("OK"))
}

// NewServiceHookHandler creates a an HTTP handler for Service Hooks
func NewServiceHookHandler(args args.Args, config config.File, k8sClient kubernetes.ClientAsync) ServiceHookHandler {
	return ServiceHookHandler{
		args:      args,
		config:    config,
		k8sClient: k8sClient,
	}
}
