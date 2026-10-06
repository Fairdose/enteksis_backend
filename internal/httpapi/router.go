package httpapi

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/Fairdose/enteksis_backend/internal/service"
)

const maxRequestBodySize = 16 << 10

var validServices = map[string]struct{}{
	"web-design":           {},
	"software-development": {},
	"digital-consulting":   {},
	"support-maintenance":  {},
}

type Dependencies struct {
	Logger         *slog.Logger
	RequestStore   service.RequestStore
	AdminStore     service.AdminRequestStore
	AllowedOrigins []string
	AdminUsername  string
	AdminPassword  string
}

type handler struct {
	logger         *slog.Logger
	requestStore   service.RequestStore
	adminStore     service.AdminRequestStore
	allowedOrigins map[string]struct{}
	adminAuth      string
}

type createRequestPayload struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	ServiceType string `json:"serviceType"`
	Description string `json:"description"`
}

type errorResponse struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields,omitempty"`
}

func NewRouter(dependencies Dependencies) http.Handler {
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	h := &handler{
		logger:         logger,
		requestStore:   dependencies.RequestStore,
		adminStore:     dependencies.AdminStore,
		allowedOrigins: make(map[string]struct{}, len(dependencies.AllowedOrigins)),
		adminAuth:      "Basic " + base64.StdEncoding.EncodeToString([]byte(dependencies.AdminUsername+":"+dependencies.AdminPassword)),
	}
	for _, origin := range dependencies.AllowedOrigins {
		h.allowedOrigins[origin] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("POST /api/v1/requests", h.createRequest)
	mux.Handle("GET /api/v1/admin/requests", h.requireAdmin(http.HandlerFunc(h.listRequests)))
	mux.Handle("GET /api/v1/admin/requests/{id}", h.requireAdmin(http.HandlerFunc(h.getRequest)))

	return h.cors(mux)
}

func (h *handler) listRequests(w http.ResponseWriter, request *http.Request) {
	if h.adminStore == nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "Talep deposu yapılandırılmadı."})
		return
	}
	requests, err := h.adminStore.ListRequests(request.Context())
	if err != nil {
		h.logger.Error("service requests could not be listed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "Talepler yüklenemedi."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": requests})
}

func (h *handler) getRequest(w http.ResponseWriter, request *http.Request) {
	serviceRequest, err := h.findRequest(request)
	if err != nil {
		h.writeRequestLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, serviceRequest)
}

func (h *handler) findRequest(request *http.Request) (service.ServiceRequest, error) {
	if h.adminStore == nil {
		return service.ServiceRequest{}, errors.New("admin request store is not configured")
	}
	return h.adminStore.GetRequest(request.Context(), request.PathValue("id"))
}

func (h *handler) writeRequestLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrRequestNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "Talep bulunamadı."})
		return
	}
	h.logger.Error("service request could not be loaded", "error", err)
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "Talep yüklenemedi."})
}

func (h *handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		provided := request.Header.Get("Authorization")
		if len(provided) != len(h.adminAuth) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.adminAuth)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Enteksis Admin", charset="UTF-8"`)
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "Yönetici erişimi gerekli."})
			return
		}
		next.ServeHTTP(w, request)
	})
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) createRequest(w http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(w, request.Body, maxRequestBodySize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var payload createRequestPayload
	if err := decoder.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Geçersiz istek gövdesi."})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "İstek tek bir JSON nesnesi içermelidir."})
		return
	}

	serviceRequest := service.Request{
		Name:        strings.TrimSpace(payload.Name),
		Email:       strings.ToLower(strings.TrimSpace(payload.Email)),
		ServiceType: strings.TrimSpace(payload.ServiceType),
		Description: strings.TrimSpace(payload.Description),
	}
	if fieldErrors := validateRequest(serviceRequest); len(fieldErrors) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
			Error:  "Lütfen işaretli alanları kontrol edin.",
			Fields: fieldErrors,
		})
		return
	}
	if h.requestStore == nil {
		h.logger.Error("request store is not configured")
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "Talebiniz şu anda kaydedilemedi."})
		return
	}

	created, err := h.requestStore.CreateRequest(request.Context(), serviceRequest)
	if err != nil {
		h.logger.Error("service request could not be stored", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "Talebiniz şu anda kaydedilemedi."})
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

func validateRequest(request service.Request) map[string]string {
	errorsByField := make(map[string]string)
	if length := utf8.RuneCountInString(request.Name); length < 2 || length > 100 {
		errorsByField["name"] = "İsim 2–100 karakter arasında olmalıdır."
	}
	if len(request.Email) > 254 || !validEmail(request.Email) {
		errorsByField["email"] = "Geçerli bir e-posta adresi girin."
	}
	if _, ok := validServices[request.ServiceType]; !ok {
		errorsByField["serviceType"] = "Listeden geçerli bir hizmet seçin."
	}
	if length := utf8.RuneCountInString(request.Description); length < 10 || length > 2000 {
		errorsByField["description"] = "Açıklama 10–2000 karakter arasında olmalıdır."
	}
	return errorsByField
}

func validEmail(value string) bool {
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && strings.Contains(value, "@")
}

func (h *handler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		_, allowed := h.allowedOrigins[origin]
		if origin != "" && allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if request.Method == http.MethodOptions {
			if !allowed {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, request)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
