package iec61850

// #include <iec61850_client.h>
// #include <mms_client_connection.h>
import "C"
import "errors"

var (
	ErrNotConnected                      = errors.New("the service request can not be executed because the client is not yet connected")
	ErrAlreadyConnected                  = errors.New("connect service not execute because the client is already connected")
	ErrConnectionLost                    = errors.New("the service request can not be executed caused by a loss of connection")
	ErrServiceNotSupported               = errors.New("the service or some given parameters are not supported by the client stack or by the server")
	ErrConnectionRejected                = errors.New("connection rejected by server")
	ErrOutstandingCallLimitReached       = errors.New("cannot send request because outstanding call limit is reached")
	ErrUserProvidedInvalidArgument       = errors.New("API function has been called with an invalid argument")
	ErrEnableReportFailedDatasetMismatch = errors.New("API function has been called with an invalid argument")
	ErrObjectReferenceInvalid            = errors.New("the object provided object reference is invalid (there is a syntactical error)")
	ErrUnexpectedValueReceived           = errors.New("received object is of unexpected type")
	ErrTimeout                           = errors.New("the communication to the server failed with a timeout")
	ErrAccessDenied                      = errors.New("the server rejected the access to the requested object/service due to access control")
	ErrObjectDoesNotExist                = errors.New("the server reported that the requested object does not exist (returned by server)")
	ErrObjectExists                      = errors.New("the server reported that the requested object already exists")
	ErrObjectAccessUnsupported           = errors.New("the server does not support the requested access method (returned by server)")
	ErrTypeInconsistent                  = errors.New("the server expected an object of another type (returned by server)")
	ErrTemporarilyUnavailable            = errors.New("the object or service is temporarily unavailable (returned by server)")
	ErrObjectUndefined                   = errors.New("the specified object is not defined in the server (returned by server)")
	ErrInvalidAddress                    = errors.New("the specified address is invalid (returned by server)")
	ErrHardwareFault                     = errors.New("service failed due to a hardware fault (returned by server)")
	ErrTypeUnsupported                   = errors.New("the requested data type is not supported by the server (returned by server)")
	ErrObjectAttributeInconsistent       = errors.New("the provided attributes are inconsistent (returned by server)")
	ErrObjectValueInvalid                = errors.New("the provided object value is invalid (returned by server)")
	ErrObjectInvalidated                 = errors.New("the object is invalidated (returned by server)")
	ErrMalformedMessage                  = errors.New("received an invalid response message from the server")
	ErrServiceNotImplemented             = errors.New("service not implemented")
	ErrUnknown                           = errors.New("unknown error")
	ErrStructureMustBeMmsValue           = errors.New("structure type must be MmsValue array")
	ErrCreateControlObjectClientFail     = errors.New("control object not found in server")
	ErrControlObjectFail                 = errors.New("control object fail")
	ErrControlSelectFail                 = errors.New("select control fail")
	ErrUnSupportedOperation              = errors.New("unsupported operation")
	ErrReadDataAccess                    = errors.New("data access error")
	ErrNullPointer                       = errors.New("null pointer returned from C function")
)

// GetMmsError maps a C MmsError to a Go error. Used by low-level MMS client APIs.
func GetMmsError(err C.MmsError) error {
	if err == C.MMS_ERROR_NONE {
		return nil
	}
	// Map MmsError to existing IedClientError-style errors where possible
	switch err {
	case C.MMS_ERROR_CONNECTION_REJECTED:
		return ErrConnectionRejected
	case C.MMS_ERROR_CONNECTION_LOST:
		return ErrConnectionLost
	case C.MMS_ERROR_SERVICE_TIMEOUT:
		return ErrTimeout
	case C.MMS_ERROR_INVALID_ARGUMENTS:
		return ErrUserProvidedInvalidArgument
	case C.MMS_ERROR_OUTSTANDING_CALL_LIMIT:
		return ErrOutstandingCallLimitReached
	case C.MMS_ERROR_ACCESS_OBJECT_ACCESS_DENIED:
		return ErrAccessDenied
	case C.MMS_ERROR_ACCESS_OBJECT_NON_EXISTENT:
		return ErrObjectDoesNotExist
	case C.MMS_ERROR_DEFINITION_OBJECT_EXISTS:
		return ErrObjectExists
	case C.MMS_ERROR_ACCESS_OBJECT_ACCESS_UNSUPPORTED:
		return ErrObjectAccessUnsupported
	case C.MMS_ERROR_DEFINITION_TYPE_INCONSISTENT:
		return ErrTypeInconsistent
	case C.MMS_ERROR_ACCESS_TEMPORARILY_UNAVAILABLE:
		return ErrTemporarilyUnavailable
	case C.MMS_ERROR_DEFINITION_OBJECT_UNDEFINED:
		return ErrObjectUndefined
	case C.MMS_ERROR_DEFINITION_INVALID_ADDRESS:
		return ErrInvalidAddress
	case C.MMS_ERROR_HARDWARE_FAULT:
		return ErrHardwareFault
	case C.MMS_ERROR_DEFINITION_TYPE_UNSUPPORTED:
		return ErrTypeUnsupported
	case C.MMS_ERROR_DEFINITION_OBJECT_ATTRIBUTE_INCONSISTENT:
		return ErrObjectAttributeInconsistent
	case C.MMS_ERROR_ACCESS_OBJECT_VALUE_INVALID:
		return ErrObjectValueInvalid
	case C.MMS_ERROR_ACCESS_OBJECT_INVALIDATED:
		return ErrObjectInvalidated
	case C.MMS_ERROR_PARSING_RESPONSE:
		return ErrMalformedMessage
	default:
		return ErrUnknown
	}
}

func GetIedClientError(err C.IedClientError) error {
	cError := C.IedClientError(err)
	switch cError {
	case C.IED_ERROR_OK:
		return nil
	case C.IED_ERROR_NOT_CONNECTED:
		return ErrNotConnected
	case C.IED_ERROR_ALREADY_CONNECTED:
		return ErrAlreadyConnected
	case C.IED_ERROR_CONNECTION_LOST:
		return ErrConnectionLost
	case C.IED_ERROR_SERVICE_NOT_SUPPORTED:
		return ErrServiceNotSupported
	case C.IED_ERROR_CONNECTION_REJECTED:
		return ErrConnectionRejected
	case C.IED_ERROR_OUTSTANDING_CALL_LIMIT_REACHED:
		return ErrOutstandingCallLimitReached
	case C.IED_ERROR_USER_PROVIDED_INVALID_ARGUMENT:
		return ErrUserProvidedInvalidArgument
	case C.IED_ERROR_ENABLE_REPORT_FAILED_DATASET_MISMATCH:
		return ErrEnableReportFailedDatasetMismatch
	case C.IED_ERROR_OBJECT_REFERENCE_INVALID:
		return ErrObjectReferenceInvalid
	case C.IED_ERROR_UNEXPECTED_VALUE_RECEIVED:
		return ErrUnexpectedValueReceived
	case C.IED_ERROR_TIMEOUT:
		return ErrTimeout
	case C.IED_ERROR_ACCESS_DENIED:
		return ErrAccessDenied
	case C.IED_ERROR_OBJECT_DOES_NOT_EXIST:
		return ErrObjectDoesNotExist
	case C.IED_ERROR_OBJECT_EXISTS:
		return ErrObjectExists
	case C.IED_ERROR_OBJECT_ACCESS_UNSUPPORTED:
		return ErrObjectAccessUnsupported
	case C.IED_ERROR_TYPE_INCONSISTENT:
		return ErrTypeInconsistent
	case C.IED_ERROR_TEMPORARILY_UNAVAILABLE:
		return ErrTemporarilyUnavailable
	case C.IED_ERROR_OBJECT_UNDEFINED:
		return ErrObjectUndefined
	case C.IED_ERROR_INVALID_ADDRESS:
		return ErrInvalidAddress
	case C.IED_ERROR_HARDWARE_FAULT:
		return ErrHardwareFault
	case C.IED_ERROR_TYPE_UNSUPPORTED:
		return ErrTypeUnsupported
	case C.IED_ERROR_OBJECT_ATTRIBUTE_INCONSISTENT:
		return ErrObjectAttributeInconsistent
	case C.IED_ERROR_OBJECT_VALUE_INVALID:
		return ErrObjectValueInvalid
	case C.IED_ERROR_OBJECT_INVALIDATED:
		return ErrObjectInvalidated
	case C.IED_ERROR_MALFORMED_MESSAGE:
		return ErrMalformedMessage
	case C.IED_ERROR_SERVICE_NOT_IMPLEMENTED:
		return ErrServiceNotImplemented
	default:
		return ErrUnknown
	}
}
