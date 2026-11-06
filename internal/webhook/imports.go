// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

// Import is the data of an import event (decision 92): the import's ID, its source and its target, and counts. It holds
// no file name, no folder name and no error text, only the error's code, as the comment events hold no comment text.
// The receiver reads the names through the API, where the reader's rights are checked.
type Import struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	FolderID string `json:"folder_id"`
	// Entries, Files and Bytes are the archive's counts, as far as the job has read them. A started event has them at 0
	// when the scan has not run; a completed one has the entries that were made or skipped.
	Entries int   `json:"entries"`
	Files   int   `json:"files"`
	Bytes   int64 `json:"bytes"`
	Skipped int   `json:"skipped"`
	// ErrorCode is the API code of a failed import, such as invalid_path or quota_exceeded; empty otherwise.
	ErrorCode string `json:"error_code,omitempty"`
}
