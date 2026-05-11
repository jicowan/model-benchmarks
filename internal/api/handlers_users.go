package api

// PRD-45: user management. Admin-only endpoints that proxy Cognito's
// admin APIs (list, invite, change role, disable/enable, reset
// password, delete). Self-mutation guards prevent an admin from
// locking themselves out. Every mutation flows through s.audit so
// actions land in config_audit_log.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"

	"github.com/accelbench/accelbench/internal/auth"

	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	smithy "github.com/aws/smithy-go"
)

// customRoleAttr is the attribute key Cognito stores the role under.
// Terraform declares the schema as `name = "role"` but Cognito exposes
// it as `custom:role` over the wire — confirmed against the live pool.
const customRoleAttr = "custom:role"

// listUsersMaxLimit matches Cognito's ListUsers per-call cap.
const listUsersMaxLimit = 60

// emailRe is a lenient email sanity check. Cognito does its own
// validation; this guard exists to reject obvious trash before we
// spend a round-trip.
var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// UserRow is the shape returned for list/create/update/disable/enable/
// reset responses. Mirrors the subset of Cognito fields the UI needs.
type UserRow struct {
	Sub            string `json:"sub"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      string `json:"created_at"`
	LastModifiedAt string `json:"last_modified_at"`
}

type listUsersResponse struct {
	Rows      []UserRow `json:"rows"`
	NextToken string    `json:"next_token,omitempty"`
}

type createUserRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type updateUserRoleRequest struct {
	Role string `json:"role"`
}

// attrValue returns the value of the named attribute, or "".
func attrValue(attrs []types.AttributeType, name string) string {
	for _, a := range attrs {
		if aws.ToString(a.Name) == name {
			return aws.ToString(a.Value)
		}
	}
	return ""
}

func rowFromListUser(u types.UserType) UserRow {
	row := UserRow{
		Sub:     attrValue(u.Attributes, "sub"),
		Email:   attrValue(u.Attributes, "email"),
		Role:    attrValue(u.Attributes, customRoleAttr),
		Status:  string(u.UserStatus),
		Enabled: u.Enabled,
	}
	if row.Sub == "" {
		// Fall back to Username, which for this pool (email-as-username)
		// is itself the sub UUID.
		row.Sub = aws.ToString(u.Username)
	}
	if u.UserCreateDate != nil {
		row.CreatedAt = u.UserCreateDate.UTC().Format("2006-01-02T15:04:05Z")
	}
	if u.UserLastModifiedDate != nil {
		row.LastModifiedAt = u.UserLastModifiedDate.UTC().Format("2006-01-02T15:04:05Z")
	}
	return row
}

func rowFromAdminGet(out *cip.AdminGetUserOutput) UserRow {
	row := UserRow{
		Sub:     attrValue(out.UserAttributes, "sub"),
		Email:   attrValue(out.UserAttributes, "email"),
		Role:    attrValue(out.UserAttributes, customRoleAttr),
		Status:  string(out.UserStatus),
		Enabled: out.Enabled,
	}
	if row.Sub == "" {
		row.Sub = aws.ToString(out.Username)
	}
	if out.UserCreateDate != nil {
		row.CreatedAt = out.UserCreateDate.UTC().Format("2006-01-02T15:04:05Z")
	}
	if out.UserLastModifiedDate != nil {
		row.LastModifiedAt = out.UserLastModifiedDate.UTC().Format("2006-01-02T15:04:05Z")
	}
	return row
}

func rowFromAdminCreate(out *cip.AdminCreateUserOutput) UserRow {
	if out.User == nil {
		return UserRow{}
	}
	return rowFromListUser(*out.User)
}

// handleListUsers: GET /api/v1/users?limit=&next_token=&filter=
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	limit := int32(listUsersMaxLimit)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > listUsersMaxLimit {
			writeError(w, http.StatusBadRequest, "invalid limit (1-60)")
			return
		}
		limit = int32(n)
	}
	in := &cip.ListUsersInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Limit:      aws.Int32(limit),
	}
	if tok := r.URL.Query().Get("next_token"); tok != "" {
		in.PaginationToken = aws.String(tok)
	}
	if f := r.URL.Query().Get("filter"); f != "" {
		in.Filter = aws.String(fmt.Sprintf(`email ^= %q`, f))
	}
	out, err := s.cognitoIDP.ListUsers(r.Context(), in)
	if err != nil {
		mapCognitoAdminError(w, err, "list users")
		return
	}
	rows := make([]UserRow, 0, len(out.Users))
	for _, u := range out.Users {
		rows = append(rows, rowFromListUser(u))
	}
	resp := listUsersResponse{Rows: rows, NextToken: aws.ToString(out.PaginationToken)}
	writeJSON(w, http.StatusOK, resp)
}

// handleCreateUser: POST /api/v1/users
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !emailRe.MatchString(req.Email) {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if req.Role != "admin" && req.Role != "user" {
		writeError(w, http.StatusBadRequest, "role must be 'admin' or 'user'")
		return
	}
	out, err := s.cognitoIDP.AdminCreateUser(r.Context(), &cip.AdminCreateUserInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(req.Email),
		UserAttributes: []types.AttributeType{
			{Name: aws.String("email"), Value: aws.String(req.Email)},
			{Name: aws.String("email_verified"), Value: aws.String("true")},
			{Name: aws.String(customRoleAttr), Value: aws.String(req.Role)},
		},
		DesiredDeliveryMediums: []types.DeliveryMediumType{types.DeliveryMediumTypeEmail},
	})
	if err != nil {
		mapCognitoAdminError(w, err, "create user")
		return
	}
	s.audit(r.Context(), "POST /api/v1/users", fmt.Sprintf("invited %s as %s", req.Email, req.Role))
	writeJSON(w, http.StatusOK, rowFromAdminCreate(out))
}

// handleUpdateUserRole: PATCH /api/v1/users/{sub}
func (s *Server) handleUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	sub := r.PathValue("sub")
	if sub == "" {
		writeError(w, http.StatusBadRequest, "missing sub")
		return
	}
	var req updateUserRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Role != "admin" && req.Role != "user" {
		writeError(w, http.StatusBadRequest, "role must be 'admin' or 'user'")
		return
	}
	p := auth.PrincipalFromContext(r.Context())
	// Self-demote guard: an admin cannot downgrade themselves.
	if p != nil && p.Sub == sub && p.Role == "admin" && req.Role == "user" {
		writeError(w, http.StatusBadRequest, "cannot demote yourself; have another admin perform this change")
		return
	}
	cur, err := s.cognitoIDP.AdminGetUser(r.Context(), &cip.AdminGetUserInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(sub),
	})
	if err != nil {
		mapCognitoAdminError(w, err, "get user")
		return
	}
	oldRow := rowFromAdminGet(cur)
	if _, err := s.cognitoIDP.AdminUpdateUserAttributes(r.Context(), &cip.AdminUpdateUserAttributesInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(sub),
		UserAttributes: []types.AttributeType{
			{Name: aws.String(customRoleAttr), Value: aws.String(req.Role)},
		},
	}); err != nil {
		mapCognitoAdminError(w, err, "update user")
		return
	}
	oldRole := oldRow.Role
	if oldRole == "" {
		oldRole = "(unset)"
	}
	s.audit(r.Context(), "PATCH /api/v1/users/"+sub,
		fmt.Sprintf("role %s: %s → %s", oldRow.Email, oldRole, req.Role))
	// Return the updated view.
	updated := oldRow
	updated.Role = req.Role
	writeJSON(w, http.StatusOK, updated)
}

// handleDisableUser: POST /api/v1/users/{sub}/disable
func (s *Server) handleDisableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserEnabled(w, r, false)
}

// handleEnableUser: POST /api/v1/users/{sub}/enable
func (s *Server) handleEnableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserEnabled(w, r, true)
}

func (s *Server) setUserEnabled(w http.ResponseWriter, r *http.Request, enable bool) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	sub := r.PathValue("sub")
	if sub == "" {
		writeError(w, http.StatusBadRequest, "missing sub")
		return
	}
	if !enable {
		// Disable-self guard. Enable-self has no guard — if you can call
		// the API you're already enabled.
		if p := auth.PrincipalFromContext(r.Context()); p != nil && p.Sub == sub {
			writeError(w, http.StatusBadRequest, "cannot disable yourself")
			return
		}
	}
	var callErr error
	if enable {
		_, callErr = s.cognitoIDP.AdminEnableUser(r.Context(), &cip.AdminEnableUserInput{
			UserPoolId: aws.String(s.authConfig.UserPoolID),
			Username:   aws.String(sub),
		})
	} else {
		_, callErr = s.cognitoIDP.AdminDisableUser(r.Context(), &cip.AdminDisableUserInput{
			UserPoolId: aws.String(s.authConfig.UserPoolID),
			Username:   aws.String(sub),
		})
	}
	if callErr != nil {
		mapCognitoAdminError(w, callErr, "toggle user enabled")
		return
	}
	cur, err := s.cognitoIDP.AdminGetUser(r.Context(), &cip.AdminGetUserInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(sub),
	})
	if err != nil {
		mapCognitoAdminError(w, err, "get user")
		return
	}
	row := rowFromAdminGet(cur)
	verb := "enable"
	if !enable {
		verb = "disable"
	}
	s.audit(r.Context(), "POST /api/v1/users/"+sub+"/"+verb, verb+" "+row.Email)
	writeJSON(w, http.StatusOK, row)
}

// handleResetUserPassword: POST /api/v1/users/{sub}/reset-password
func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	sub := r.PathValue("sub")
	if sub == "" {
		writeError(w, http.StatusBadRequest, "missing sub")
		return
	}
	if _, err := s.cognitoIDP.AdminResetUserPassword(r.Context(), &cip.AdminResetUserPasswordInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(sub),
	}); err != nil {
		mapCognitoAdminError(w, err, "reset password")
		return
	}
	cur, err := s.cognitoIDP.AdminGetUser(r.Context(), &cip.AdminGetUserInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(sub),
	})
	if err != nil {
		mapCognitoAdminError(w, err, "get user")
		return
	}
	row := rowFromAdminGet(cur)
	s.audit(r.Context(), "POST /api/v1/users/"+sub+"/reset-password", "password-reset "+row.Email)
	writeJSON(w, http.StatusOK, row)
}

// handleResendInvite: POST /api/v1/users/{sub}/resend-invite
//
// Re-issues the initial invitation email to a user still in the
// FORCE_CHANGE_PASSWORD state (never completed first login). Cognito
// won't honor AdminResetUserPassword on those users when the pool has
// no self-service recovery mechanism, so this is the admin-correct way
// to get them back a temporary password.
//
// Uses AdminCreateUser with MessageActionType=RESEND, which Cognito
// treats as "find the existing user, regenerate a temp password, and
// re-send the invite email."
func (s *Server) handleResendInvite(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	sub := r.PathValue("sub")
	if sub == "" {
		writeError(w, http.StatusBadRequest, "missing sub")
		return
	}
	// Cognito's RESEND flow expects the email (the attribute the user was
	// originally created with) as Username, not the sub UUID. Look up the
	// email first.
	cur, err := s.cognitoIDP.AdminGetUser(r.Context(), &cip.AdminGetUserInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(sub),
	})
	if err != nil {
		mapCognitoAdminError(w, err, "get user")
		return
	}
	email := attrValue(cur.UserAttributes, "email")
	if email == "" {
		writeError(w, http.StatusBadRequest, "user has no email attribute")
		return
	}
	if _, err := s.cognitoIDP.AdminCreateUser(r.Context(), &cip.AdminCreateUserInput{
		UserPoolId:             aws.String(s.authConfig.UserPoolID),
		Username:               aws.String(email),
		MessageAction:          types.MessageActionTypeResend,
		DesiredDeliveryMediums: []types.DeliveryMediumType{types.DeliveryMediumTypeEmail},
	}); err != nil {
		mapCognitoAdminError(w, err, "resend invite")
		return
	}
	s.audit(r.Context(), "POST /api/v1/users/"+sub+"/resend-invite", "resend-invite "+email)
	writeJSON(w, http.StatusOK, rowFromAdminGet(cur))
}

// handleDeleteUser: DELETE /api/v1/users/{sub}
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	sub := r.PathValue("sub")
	if sub == "" {
		writeError(w, http.StatusBadRequest, "missing sub")
		return
	}
	if p := auth.PrincipalFromContext(r.Context()); p != nil && p.Sub == sub {
		writeError(w, http.StatusBadRequest, "cannot delete yourself; have another admin perform this action")
		return
	}
	// Fetch email first so the audit record is useful after the user is
	// gone. Best-effort — fall back to the sub if AdminGetUser fails.
	email := sub
	if cur, err := s.cognitoIDP.AdminGetUser(r.Context(), &cip.AdminGetUserInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(sub),
	}); err == nil {
		if e := attrValue(cur.UserAttributes, "email"); e != "" {
			email = e
		}
	}
	if _, err := s.cognitoIDP.AdminDeleteUser(r.Context(), &cip.AdminDeleteUserInput{
		UserPoolId: aws.String(s.authConfig.UserPoolID),
		Username:   aws.String(sub),
	}); err != nil {
		mapCognitoAdminError(w, err, "delete user")
		return
	}
	s.audit(r.Context(), "DELETE /api/v1/users/"+sub, "delete "+email)
	w.WriteHeader(http.StatusNoContent)
}

// mapCognitoAdminError is the admin-API analogue of mapCognitoAuthError.
// Distinguishes "not found" / "duplicate" / "bad input" from generic
// upstream failures so the UI can show something useful.
func mapCognitoAdminError(w http.ResponseWriter, err error, context string) {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "UserNotFoundException":
			writeError(w, http.StatusNotFound, "user not found")
			return
		case "UsernameExistsException":
			writeError(w, http.StatusConflict, "user already exists")
			return
		case "InvalidParameterException", "InvalidLambdaResponseException":
			writeError(w, http.StatusBadRequest, apiErr.ErrorMessage())
			return
		case "TooManyRequestsException":
			writeError(w, http.StatusTooManyRequests, "cognito rate limit; retry")
			return
		}
		// Pass through the Cognito error code + message so operators can
		// see *why* an action failed. Previously we returned "upstream_error"
		// for everything unrecognized, which hid e.g. "NotAuthorizedException:
		// This userpool does not have password recovery mechanism" — the
		// message that surfaces when AdminResetUserPassword is called on a
		// user still in FORCE_CHANGE_PASSWORD state.
		log.Printf("cognito %s: %s: %s", context, apiErr.ErrorCode(), apiErr.ErrorMessage())
		writeError(w, http.StatusBadGateway,
			fmt.Sprintf("%s: %s", apiErr.ErrorCode(), apiErr.ErrorMessage()))
		return
	}
	// Non-APIError failure (network, SDK bug). Log the raw error and
	// return generic.
	log.Printf("cognito %s: %v", context, err)
	writeError(w, http.StatusBadGateway, "upstream_error")
}

