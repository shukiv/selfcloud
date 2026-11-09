// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strconv"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// A file list takes three filters (decision 110): ?type= (one value: a file group ID, or folder or shortcut where the
// list holds those kinds), ?modified_after= and ?modified_before= (milliseconds since the epoch: after inclusive, before
// exclusive). Every value is checked here, before SQL: a type is read through the table, and a range must have its start
// before its end. A bad value is invalid_request.

const (
	paramType           = "type"
	paramModifiedAfter  = "modified_after"
	paramModifiedBefore = "modified_before"
)

// filterBad is the one answer to a filter value that is not valid.
var filterBad = apierr.New(apierr.InvalidRequest, "a type, or a modified_after and modified_before range, is not valid")

// listFilterParams reads the filter of a list from the request's query. The zero filter is returned when none is given.
func listFilterParams(r *http.Request) (store.ListFilter, error) {
	q := r.URL.Query()
	if len(q[paramType]) > 1 {
		return store.ListFilter{}, filterBad
	}
	f, err := store.ListTypeFilter(q.Get(paramType))
	if err != nil {
		return store.ListFilter{}, filterBad
	}
	if f.ModAfter, err = modifiedBound(q, paramModifiedAfter); err != nil {
		return store.ListFilter{}, err
	}
	if f.ModBefore, err = modifiedBound(q, paramModifiedBefore); err != nil {
		return store.ListFilter{}, err
	}
	if err := f.Validate(); err != nil {
		return store.ListFilter{}, filterBad
	}
	return f, nil
}

// modifiedBound reads one modified bound: absent is nil, and a value that is not a non-negative integer is filterBad.
func modifiedBound(q map[string][]string, name string) (*int64, error) {
	vals := q[name]
	if len(vals) == 0 {
		return nil, nil
	}
	if len(vals) > 1 {
		return nil, filterBad
	}
	v, err := strconv.ParseInt(vals[0], 10, 64)
	if err != nil || v < 0 {
		return nil, filterBad
	}
	return &v, nil
}
