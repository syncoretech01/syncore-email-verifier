package emailverifier

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// Standard Errors
	ErrTimeout           = "The connection to the mail server has timed out"
	ErrNoSuchHost        = "Mail server does not exist"
	ErrServerUnavailable = "Mail server is unavailable"
	ErrBlocked           = "Blocked by mail server"
	ErrConnRefused       = "Connection refused by the mail server"

	// RCPT Errors
	ErrTryAgainLater           = "Try again later"
	ErrFullInbox               = "Recipient out of disk space"
	ErrTooManyRCPT             = "Too many recipients"
	ErrNoRelay                 = "Not an open relay"
	ErrMailboxBusy             = "Mailbox busy"
	ErrExceededMessagingLimits = "Messaging limits have been exceeded"
	ErrNotAllowed              = "Not Allowed"
	ErrNeedMAILBeforeRCPT      = "Need MAIL before RCPT"
	ErrRCPTHasMoved            = "Recipient has moved"
)

// LookupError is an MX dns records lookup error
type LookupError struct {
	Message string `json:"message" xml:"message"`
	Details string `json:"details" xml:"details"`
}

// newLookupError creates a new LookupError reference and returns it
func newLookupError(message, details string) *LookupError {
	return &LookupError{message, details}
}

func (e *LookupError) Error() string {
	return fmt.Sprintf("%s : %s", e.Message, e.Details)
}

// ParseSMTPError receives an MX Servers response message
// and generates the corresponding MX error
func ParseSMTPError(err error) *LookupError {
	errStr := err.Error()

	// Verify the length of the error before reading nil indexes
	if len(errStr) < 3 {
		return parseBasicErr(err)
	}

	// Strips out the status code string and converts to an integer for parsing
	status, convErr := strconv.Atoi(string([]rune(errStr)[0:3]))
	if convErr != nil {
		return parseBasicErr(err)
	}

	// If the status code is above 400 there was an error and we should return it
	if status > 400 {
		// Don't return an error if the error contains anything about the address
		// being undeliverable
		if insContains(errStr,
			"undeliverable",
			"does not exist",
			"may not exist",
			"user unknown",
			"user not found",
			"invalid address",
			"recipient invalid",
			"recipient rejected",
			"address rejected",
			"no mailbox") {
			return newLookupError(ErrServerUnavailable, errStr)
		}

		switch status {
		case 421:
			return newLookupError(ErrTryAgainLater, errStr)
		case 450:
			return newLookupError(ErrMailboxBusy, errStr)
		case 451:
			return newLookupError(ErrExceededMessagingLimits, errStr)
		case 452:
			if insContains(errStr,
				"full",
				"space",
				"over quota",
				"insufficient",
			) {
				return newLookupError(ErrFullInbox, errStr)
			}
			return newLookupError(ErrTooManyRCPT, errStr)
		case 503:
			return newLookupError(ErrNeedMAILBeforeRCPT, errStr)
		case 550: // 550 is Mailbox Unavailable - usually undeliverable, ref: https://blog.mailtrap.io/550-5-1-1-rejected-fix/
			if insContains(errStr,
				"spamhaus",
				"proofpoint",
				"cloudmark",
				"banned",
				"blacklisted",
				"blocked",
				"block list",
				"denied") {
				return newLookupError(ErrBlocked, errStr)
			}
			return newLookupError(ErrServerUnavailable, errStr)
		case 551:
			return newLookupError(ErrRCPTHasMoved, errStr)
		case 552:
			return newLookupError(ErrFullInbox, errStr)
		case 553:
			return newLookupError(ErrNoRelay, errStr)
		case 554:
			return newLookupError(ErrNotAllowed, errStr)
		default:
			return parseBasicErr(err)
		}
	}
	return nil
}

// parseBasicErr parses a basic MX record response and returns
// a more understandable LookupError
func parseBasicErr(err error) *LookupError {
	errStr := err.Error()

	// Return a more understandable error
	switch {
	case insContains(errStr,
		"spamhaus",
		"proofpoint",
		"cloudmark",
		"banned",
		"blocked",
		"denied"):
		return newLookupError(ErrBlocked, errStr)
	case insContains(errStr, "connection refused"):
		return newLookupError(ErrConnRefused, errStr)
	case insContains(errStr, "timeout", "i/o timeout", "deadline exceeded"):
		return newLookupError(ErrTimeout, errStr)
	case insContains(errStr, "no such host"):
		return newLookupError(ErrNoSuchHost, errStr)
	case insContains(errStr, "unavailable"):
		return newLookupError(ErrServerUnavailable, errStr)
	default:
		return newLookupError(errStr, errStr)
	}
}

// Phrase buckets used to normalize a recipient (RCPT) reply. Kept deliberately
// tight: only strong, explicit nonexistence phrases may prove mailbox_not_found.
var (
	// strongNonexistentPhrases prove the recipient does not exist.
	strongNonexistentPhrases = []string{
		"user unknown",
		"no such user",
		"no such mailbox",
		"recipient does not exist",
		"address does not exist",
		"invalid recipient",
		"recipient not found",
		"account not found",
	}
	// disabledPhrases indicate an existing-but-unusable mailbox.
	disabledPhrases = []string{
		"disabled",
		"suspended",
		"inactive",
		"deactivated",
		"no longer active",
	}
	// quotaPhrases indicate a full / over-quota mailbox.
	quotaPhrases = []string{
		"quota",
		"full",
		"insufficient",
		"storage",
		"over space",
		"out of space",
	}
)

// classifyRecipientReply normalizes the result of a RCPT command into structured
// recipient evidence. It returns the coarse recipient result, the normalized
// recipient reason, the sanitized numeric SMTP code (0 when there was no reply),
// and a non-nil transportErr for connection-level failures (timeout / refused)
// so those remain identifiable and never collapse into a generic unknown.
func classifyRecipientReply(err error) (result, reason string, code int, transportErr *LookupError) {
	if err == nil {
		return recipientAccepted, "", 250, nil
	}

	errStr := err.Error()
	code = parseSMTPCode(errStr)
	if code == 0 {
		// No SMTP reply code: this is a transport-level failure.
		return recipientUnknown, "", 0, parseBasicErr(err)
	}

	lower := strings.ToLower(errStr)
	switch {
	case code >= 200 && code < 300:
		return recipientAccepted, "", code, nil

	case code >= 400 && code < 500:
		switch code {
		case 421, 450:
			if insContains(lower, "greylist", "grey list", "gray list", "greylisted") {
				return recipientTemporary, reasonGreylisted, code, nil
			}
			return recipientTemporary, reasonTemporaryFailure, code, nil
		case 451:
			return recipientTemporary, reasonRateLimited, code, nil
		case 452:
			if insContains(lower, quotaPhrases...) {
				return recipientTemporary, reasonFullInbox, code, nil
			}
			return recipientTemporary, reasonRateLimited, code, nil
		default:
			return recipientTemporary, reasonTemporaryFailure, code, nil
		}

	case code >= 500:
		// Permanent quota exhaustion (552) is a full inbox, not a rejection.
		if code == 552 {
			return recipientRejected, reasonFullInbox, code, nil
		}
		// Only strong, explicit nonexistence phrases may prove mailbox_not_found.
		if insContains(lower, strongNonexistentPhrases...) {
			return recipientRejected, reasonMailboxNotFound, code, nil
		}
		if insContains(lower, disabledPhrases...) {
			return recipientRejected, reasonMailboxDisabled, code, nil
		}
		// Everything else permanent — policy, spam, reputation, blacklist, denied,
		// blocked, and any generic/ambiguous 550/554 — is a policy block.
		return recipientBlocked, reasonPolicyBlock, code, nil

	default:
		return recipientUnknown, "", code, nil
	}
}

// parseSMTPCode extracts the leading 3-digit SMTP status code, or 0 if absent.
func parseSMTPCode(s string) int {
	if len(s) < 3 {
		return 0
	}
	code, err := strconv.Atoi(string([]rune(s)[0:3]))
	if err != nil {
		return 0
	}
	return code
}

// insContains returns true if any of the substrings
// are found in the passed string. This method of checking
// contains is case insensitive
func insContains(str string, subStrs ...string) bool {
	for _, subStr := range subStrs {
		if strings.Contains(strings.ToLower(str),
			strings.ToLower(subStr)) {
			return true
		}
	}
	return false
}
