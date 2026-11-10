package sftpgo

// User mirrors the subset of the SFTPGo admin REST API "User" object that the
// host panel manages. Field names and units match SFTPGo's JSON exactly.
//
// Units:
//   - QuotaSize / UsedQuotaSize: bytes
//   - UploadBandwidth / DownloadBandwidth: KB/s
//   - LastLogin / CreatedAt / UpdatedAt: milliseconds since the Unix epoch
//     (LastLogin == 0 means the user has never logged in)
type User struct {
	Username          string              `json:"username"`
	Email             string              `json:"email,omitempty"`
	Password          string              `json:"password,omitempty"` // write-only, never returned by SFTPGo
	PublicKeys        []string            `json:"public_keys,omitempty"`
	HomeDir           string              `json:"home_dir,omitempty"`
	Permissions       map[string][]string `json:"permissions,omitempty"`
	Status            int                 `json:"status"` // 1 = enabled, 0 = disabled
	QuotaSize         int64               `json:"quota_size,omitempty"`
	QuotaFiles        int                 `json:"quota_files,omitempty"`
	UploadBandwidth   int64               `json:"upload_bandwidth,omitempty"`
	DownloadBandwidth int64               `json:"download_bandwidth,omitempty"`
	Filters           UserFilters         `json:"filters,omitempty"`
	UsedQuotaSize     int64               `json:"used_quota_size,omitempty"`
	UsedQuotaFiles    int                 `json:"used_quota_files,omitempty"`
	LastLogin         int64               `json:"last_login,omitempty"`
	CreatedAt         int64               `json:"created_at,omitempty"`
	UpdatedAt         int64               `json:"updated_at,omitempty"`
}

// UserFilters mirrors the subset of SFTPGo's user "filters" object we use.
type UserFilters struct {
	RequirePasswordChange bool `json:"require_password_change,omitempty"`
}
