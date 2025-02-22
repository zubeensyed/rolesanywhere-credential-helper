package aws_signing_helper

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/awsutil"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/private/protocol"
)

const opCreateSession = "CreateSession"

type CredentialsOpts struct {
	PrivateKeyId        string
	CertificateId       string
	CertificateBundleId string
	RoleArn             string
	ProfileArnStr       string
	TrustAnchorArnStr   string
	SessionDuration     int
	Region              string
	Endpoint            string
	NoVerifySSL         bool
	WithProxy           bool
	Debug               bool
	Version             string
}

// Function to create session and generate credentials
func GenerateCredentials(opts *CredentialsOpts) (CredentialProcessOutput, error) {
	// assign values to region and endpoint if they haven't already been assigned
	trustAnchorArn, err := arn.Parse(opts.TrustAnchorArnStr)
	if err != nil {
		return CredentialProcessOutput{}, err
	}
	profileArn, err := arn.Parse(opts.ProfileArnStr)
	if err != nil {
		return CredentialProcessOutput{}, err
	}

	if trustAnchorArn.Region != profileArn.Region {
		return CredentialProcessOutput{}, err
	}

	if opts.Region == "" {
		opts.Region = trustAnchorArn.Region
	}

	privateKey, err := ReadPrivateKeyData(opts.PrivateKeyId)
	if err != nil {
		return CredentialProcessOutput{}, err
	}
	certificateData, err := ReadCertificateData(opts.CertificateId)
	if err != nil {
		return CredentialProcessOutput{}, err
	}
	certificateDerData, err := base64.StdEncoding.DecodeString(certificateData.CertificateData)
	if err != nil {
		return CredentialProcessOutput{}, err
	}
	certificate, err := x509.ParseCertificate([]byte(certificateDerData))
	if err != nil {
		return CredentialProcessOutput{}, err
	}
	var certificateChain []x509.Certificate
	if opts.CertificateBundleId != "" {
		certificateChainPointers, err := ReadCertificateBundleData(opts.CertificateBundleId)
		if err != nil {
			return CredentialProcessOutput{}, err
		}
		for _, certificate := range certificateChainPointers {
			certificateChain = append(certificateChain, *certificate)
		}
	}

	mySession := session.Must(session.NewSession())

	var logLevel aws.LogLevelType
	if opts.Debug {
		logLevel = aws.LogDebug
	} else {
		logLevel = aws.LogOff
	}

	var tr *http.Transport
	if opts.WithProxy {
		tr = &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: opts.NoVerifySSL},
			Proxy:           http.ProxyFromEnvironment,
		}
	} else {
		tr = &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: opts.NoVerifySSL},
		}
	}
	client := &http.Client{Transport: tr}
	config := aws.NewConfig().WithRegion(opts.Region).WithHTTPClient(client).WithLogLevel(logLevel)
	if opts.Endpoint != "" {
		config.WithEndpoint(opts.Endpoint)
	}
	rolesAnywhereClient := NewClient(mySession, config)
	rolesAnywhereClient.Handlers.Build.RemoveByName("core.SDKVersionUserAgentHandler")
	rolesAnywhereClient.Handlers.Build.PushBackNamed(request.NamedHandler{Name: "v4x509.CredHelperUserAgentHandler", Fn: request.MakeAddToUserAgentHandler("CredHelper", opts.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)})
	rolesAnywhereClient.Handlers.Sign.Clear()
	rolesAnywhereClient.Handlers.Sign.PushBackNamed(request.NamedHandler{Name: "v4x509.SignRequestHandler", Fn: CreateSignFunction(privateKey, *certificate, certificateChain)})

	durationSeconds := int64(3600)
	createSessionRequest := CreateSessionInput{
		Cert:               &certificateData.CertificateData,
		ProfileArn:         &opts.ProfileArnStr,
		TrustAnchorArn:     &opts.TrustAnchorArnStr,
		DurationSeconds:    &(durationSeconds),
		InstanceProperties: nil,
		RoleArn:            &opts.RoleArn,
		SessionName:        nil,
	}
	output, err := rolesAnywhereClient.CreateSession(&createSessionRequest)
	if err != nil {
		return CredentialProcessOutput{}, err
	}

	if len(output.CredentialSet) == 0 {
		msg := "unable to obtain temporary security credentials from CreateSession"
		return CredentialProcessOutput{}, errors.New(msg)
	}
	credentials := output.CredentialSet[0].Credentials
	credentialProcessOutput := CredentialProcessOutput{
		Version:         1,
		AccessKeyId:     *credentials.AccessKeyId,
		SecretAccessKey: *credentials.SecretAccessKey,
		SessionToken:    *credentials.SessionToken,
		Expiration:      *credentials.Expiration,
	}
	return credentialProcessOutput, nil
}

// CreateSessionRequest generates a "aws/request.Request" representing the
// client's request for the CreateSession operation. The "output" return
// value will be populated with the request's response once the request completes
// successfully.
//
// Use "Send" method on the returned Request to send the API call to the service.
// the "output" return value is not valid until after Send returns without error.
//
// See CreateSession for more information on using the CreateSession
// API call, and error handling.
//
// This method is useful when you want to inject custom logic or configuration
// into the SDK's request lifecycle. Such as custom headers, or retry logic.
//
//	// Example sending a request using the CreateSessionRequest method.
//	req, resp := client.CreateSessionRequest(params)
//
//	err := req.Send()
//	if err == nil { // resp is now filled
//	    fmt.Println(resp)
//	}
//
// See also, https://docs.aws.amazon.com/goto/WebAPI/roles-anywhere-2018-05-10/CreateSession
func (c *RolesAnywhere) CreateSessionRequest(input *CreateSessionInput) (req *request.Request, output *CreateSessionOutput) {
	op := &request.Operation{
		Name:       opCreateSession,
		HTTPMethod: "POST",
		HTTPPath:   "/sessions",
	}

	if input == nil {
		input = &CreateSessionInput{}
	}

	output = &CreateSessionOutput{}
	req = c.newRequest(op, input, output)
	return
}

// CreateSession API operation for RolesAnywhere Service.
//
// Returns awserr.Error for service API and SDK errors. Use runtime type assertions
// with awserr.Error's Code and Message methods to get detailed information about
// the error.
//
// See the AWS API reference guide for RolesAnywhere Service's
// API operation CreateSession for usage and error information.
//
// Returned Error Types:
//
//   - ValidationException
//
//   - ResourceNotFoundException
//
//   - AccessDeniedException
//
// See also, https://docs.aws.amazon.com/goto/WebAPI/roles-anywhere-2018-05-10/CreateSession
func (c *RolesAnywhere) CreateSession(input *CreateSessionInput) (*CreateSessionOutput, error) {
	req, out := c.CreateSessionRequest(input)
	return out, req.Send()
}

// CreateSessionWithContext is the same as CreateSession with the addition of
// the ability to pass a context and additional request options.
//
// See CreateSession for details on how to use this API operation.
//
// The context must be non-nil and will be used for request cancellation. If
// the context is nil a panic will occur. In the future the SDK may create
// sub-contexts for http.Requests. See https://golang.org/pkg/context/
// for more information on using Contexts.
func (c *RolesAnywhere) CreateSessionWithContext(ctx aws.Context, input *CreateSessionInput, opts ...request.Option) (*CreateSessionOutput, error) {
	req, out := c.CreateSessionRequest(input)
	req.SetContext(ctx)
	req.ApplyOptions(opts...)
	return out, req.Send()
}

type CreateSessionInput struct {
	_ struct{} `type:"structure"`

	Cert *string `location:"header" locationName:"x-amz-x509" type:"string"`

	DurationSeconds *int64 `locationName:"durationSeconds" min:"900" type:"integer"`

	InstanceProperties map[string]*string `locationName:"instanceProperties" type:"map"`

	// ProfileArn is a required field
	ProfileArn *string `location:"querystring" locationName:"profileArn" type:"string" required:"true"`

	// RoleArn is a required field
	RoleArn *string `location:"querystring" locationName:"roleArn" type:"string" required:"true"`

	SessionName *string `locationName:"sessionName" min:"2" type:"string"`

	TrustAnchorArn *string `location:"querystring" locationName:"trustAnchorArn" type:"string"`
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s CreateSessionInput) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s CreateSessionInput) GoString() string {
	return s.String()
}

// Validate inspects the fields of the type to determine if they are valid.
func (s *CreateSessionInput) Validate() error {
	invalidParams := request.ErrInvalidParams{Context: "CreateSessionInput"}
	if s.DurationSeconds != nil && *s.DurationSeconds < 900 {
		invalidParams.Add(request.NewErrParamMinValue("DurationSeconds", 900))
	}
	if s.ProfileArn == nil {
		invalidParams.Add(request.NewErrParamRequired("ProfileArn"))
	}
	if s.RoleArn == nil {
		invalidParams.Add(request.NewErrParamRequired("RoleArn"))
	}
	if s.SessionName != nil && len(*s.SessionName) < 2 {
		invalidParams.Add(request.NewErrParamMinLen("SessionName", 2))
	}

	if invalidParams.Len() > 0 {
		return invalidParams
	}
	return nil
}

// SetCert sets the Cert field's value.
func (s *CreateSessionInput) SetCert(v string) *CreateSessionInput {
	s.Cert = &v
	return s
}

// SetDurationSeconds sets the DurationSeconds field's value.
func (s *CreateSessionInput) SetDurationSeconds(v int64) *CreateSessionInput {
	s.DurationSeconds = &v
	return s
}

// SetInstanceProperties sets the InstanceProperties field's value.
func (s *CreateSessionInput) SetInstanceProperties(v map[string]*string) *CreateSessionInput {
	s.InstanceProperties = v
	return s
}

// SetProfileArn sets the ProfileArn field's value.
func (s *CreateSessionInput) SetProfileArn(v string) *CreateSessionInput {
	s.ProfileArn = &v
	return s
}

// SetRoleArn sets the RoleArn field's value.
func (s *CreateSessionInput) SetRoleArn(v string) *CreateSessionInput {
	s.RoleArn = &v
	return s
}

// SetSessionName sets the SessionName field's value.
func (s *CreateSessionInput) SetSessionName(v string) *CreateSessionInput {
	s.SessionName = &v
	return s
}

// SetTrustAnchorArn sets the TrustAnchorArn field's value.
func (s *CreateSessionInput) SetTrustAnchorArn(v string) *CreateSessionInput {
	s.TrustAnchorArn = &v
	return s
}

type CreateSessionOutput struct {
	_ struct{} `type:"structure"`

	CredentialSet []*CredentialResponse `locationName:"credentialSet" type:"list"`

	EnrollmentArn *string `locationName:"enrollmentArn" type:"string"`

	SubjectArn *string `locationName:"subjectArn" type:"string"`
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s CreateSessionOutput) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s CreateSessionOutput) GoString() string {
	return s.String()
}

// SetCredentialSet sets the CredentialSet field's value.
func (s *CreateSessionOutput) SetCredentialSet(v []*CredentialResponse) *CreateSessionOutput {
	s.CredentialSet = v
	return s
}

// SetEnrollmentArn sets the EnrollmentArn field's value.
func (s *CreateSessionOutput) SetEnrollmentArn(v string) *CreateSessionOutput {
	s.EnrollmentArn = &v
	return s
}

// SetSubjectArn sets the SubjectArn field's value.
func (s *CreateSessionOutput) SetSubjectArn(v string) *CreateSessionOutput {
	s.SubjectArn = &v
	return s
}

type CredentialResponse struct {
	_ struct{} `type:"structure"`

	AssumedRoleUser *AssumedRoleUser `locationName:"assumedRoleUser" type:"structure"`

	Credentials *Credentials `locationName:"credentials" type:"structure"`

	PackedPolicySize *int64 `locationName:"packedPolicySize" type:"integer"`

	RoleArn *string `locationName:"roleArn" type:"string"`

	SourceIdentity *string `locationName:"sourceIdentity" type:"string"`
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s CredentialResponse) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s CredentialResponse) GoString() string {
	return s.String()
}

// SetAssumedRoleUser sets the AssumedRoleUser field's value.
func (s *CredentialResponse) SetAssumedRoleUser(v *AssumedRoleUser) *CredentialResponse {
	s.AssumedRoleUser = v
	return s
}

// SetCredentials sets the Credentials field's value.
func (s *CredentialResponse) SetCredentials(v *Credentials) *CredentialResponse {
	s.Credentials = v
	return s
}

// SetPackedPolicySize sets the PackedPolicySize field's value.
func (s *CredentialResponse) SetPackedPolicySize(v int64) *CredentialResponse {
	s.PackedPolicySize = &v
	return s
}

// SetRoleArn sets the RoleArn field's value.
func (s *CredentialResponse) SetRoleArn(v string) *CredentialResponse {
	s.RoleArn = &v
	return s
}

// SetSourceIdentity sets the SourceIdentity field's value.
func (s *CredentialResponse) SetSourceIdentity(v string) *CredentialResponse {
	s.SourceIdentity = &v
	return s
}

type CredentialSummary struct {
	_ struct{} `type:"structure"`

	Enabled *bool `locationName:"enabled" type:"boolean"`

	Failed *bool `locationName:"failed" type:"boolean"`

	Issuer *string `locationName:"issuer" type:"string"`

	SeenAt *time.Time `locationName:"seenAt" type:"timestamp" timestampFormat:"iso8601"`

	SerialNumber *string `locationName:"serialNumber" type:"string"`

	// X509Certificate is automatically base64 encoded/decoded by the SDK.
	X509Certificate []byte `locationName:"x509Certificate" type:"blob"`
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s CredentialSummary) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s CredentialSummary) GoString() string {
	return s.String()
}

// SetEnabled sets the Enabled field's value.
func (s *CredentialSummary) SetEnabled(v bool) *CredentialSummary {
	s.Enabled = &v
	return s
}

// SetFailed sets the Failed field's value.
func (s *CredentialSummary) SetFailed(v bool) *CredentialSummary {
	s.Failed = &v
	return s
}

// SetIssuer sets the Issuer field's value.
func (s *CredentialSummary) SetIssuer(v string) *CredentialSummary {
	s.Issuer = &v
	return s
}

// SetSeenAt sets the SeenAt field's value.
func (s *CredentialSummary) SetSeenAt(v time.Time) *CredentialSummary {
	s.SeenAt = &v
	return s
}

// SetSerialNumber sets the SerialNumber field's value.
func (s *CredentialSummary) SetSerialNumber(v string) *CredentialSummary {
	s.SerialNumber = &v
	return s
}

// SetX509Certificate sets the X509Certificate field's value.
func (s *CredentialSummary) SetX509Certificate(v []byte) *CredentialSummary {
	s.X509Certificate = v
	return s
}

type Credentials struct {
	_ struct{} `type:"structure"`

	AccessKeyId *string `locationName:"accessKeyId" type:"string"`

	Expiration *string `locationName:"expiration" type:"string"`

	// SecretAccessKey is a sensitive parameter and its value will be
	// replaced with "sensitive" in string returned by Credentials's
	// String and GoString methods.
	SecretAccessKey *string `locationName:"secretAccessKey" type:"string" sensitive:"true"`

	SessionToken *string `locationName:"sessionToken" type:"string"`
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s Credentials) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s Credentials) GoString() string {
	return s.String()
}

// SetAccessKeyId sets the AccessKeyId field's value.
func (s *Credentials) SetAccessKeyId(v string) *Credentials {
	s.AccessKeyId = &v
	return s
}

// SetExpiration sets the Expiration field's value.
func (s *Credentials) SetExpiration(v string) *Credentials {
	s.Expiration = &v
	return s
}

// SetSecretAccessKey sets the SecretAccessKey field's value.
func (s *Credentials) SetSecretAccessKey(v string) *Credentials {
	s.SecretAccessKey = &v
	return s
}

// SetSessionToken sets the SessionToken field's value.
func (s *Credentials) SetSessionToken(v string) *Credentials {
	s.SessionToken = &v
	return s
}

type AssumedRoleUser struct {
	_ struct{} `type:"structure"`

	Arn *string `locationName:"arn" type:"string"`

	AssumedRoleId *string `locationName:"assumedRoleId" type:"string"`
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s AssumedRoleUser) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s AssumedRoleUser) GoString() string {
	return s.String()
}

// SetArn sets the Arn field's value.
func (s *AssumedRoleUser) SetArn(v string) *AssumedRoleUser {
	s.Arn = &v
	return s
}

// SetAssumedRoleId sets the AssumedRoleId field's value.
func (s *AssumedRoleUser) SetAssumedRoleId(v string) *AssumedRoleUser {
	s.AssumedRoleId = &v
	return s
}

type ValidationException struct {
	_            struct{}                  `type:"structure"`
	RespMetadata protocol.ResponseMetadata `json:"-" xml:"-"`

	Message_ *string `locationName:"message" type:"string"`
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s ValidationException) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s ValidationException) GoString() string {
	return s.String()
}

func newErrorValidationException(v protocol.ResponseMetadata) error {
	return &ValidationException{
		RespMetadata: v,
	}
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s AccessDeniedException) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s AccessDeniedException) GoString() string {
	return s.String()
}

func newErrorAccessDeniedException(v protocol.ResponseMetadata) error {
	return &AccessDeniedException{
		RespMetadata: v,
	}
}

// Code returns the exception type name.
func (s *AccessDeniedException) Code() string {
	return "AccessDeniedException"
}

// Message returns the exception's message.
func (s *AccessDeniedException) Message() string {
	if s.Message_ != nil {
		return *s.Message_
	}
	return ""
}

type AccessDeniedException struct {
	_            struct{}                  `type:"structure"`
	RespMetadata protocol.ResponseMetadata `json:"-" xml:"-"`

	Message_ *string `locationName:"message" type:"string"`
}

// OrigErr always returns nil, satisfies awserr.Error interface.
func (s *AccessDeniedException) OrigErr() error {
	return nil
}

func (s *AccessDeniedException) Error() string {
	return fmt.Sprintf("%s: %s", s.Code(), s.Message())
}

// Status code returns the HTTP status code for the request's response error.
func (s *AccessDeniedException) StatusCode() int {
	return s.RespMetadata.StatusCode
}

// RequestID returns the service's response RequestID for request.
func (s *AccessDeniedException) RequestID() string {
	return s.RespMetadata.RequestID
}

type ResourceNotFoundException struct {
	_            struct{}                  `type:"structure"`
	RespMetadata protocol.ResponseMetadata `json:"-" xml:"-"`

	Message_ *string `locationName:"message" type:"string"`
}

// String returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s ResourceNotFoundException) String() string {
	return awsutil.Prettify(s)
}

// GoString returns the string representation.
//
// API parameter values that are decorated as "sensitive" in the API will not
// be included in the string output. The member name will be present, but the
// value will be replaced with "sensitive".
func (s ResourceNotFoundException) GoString() string {
	return s.String()
}

func newErrorResourceNotFoundException(v protocol.ResponseMetadata) error {
	return &ResourceNotFoundException{
		RespMetadata: v,
	}
}

// Code returns the exception type name.
func (s *ResourceNotFoundException) Code() string {
	return "ResourceNotFoundException"
}

// Message returns the exception's message.
func (s *ResourceNotFoundException) Message() string {
	if s.Message_ != nil {
		return *s.Message_
	}
	return ""
}

// OrigErr always returns nil, satisfies awserr.Error interface.
func (s *ResourceNotFoundException) OrigErr() error {
	return nil
}

func (s *ResourceNotFoundException) Error() string {
	return fmt.Sprintf("%s: %s", s.Code(), s.Message())
}

// Status code returns the HTTP status code for the request's response error.
func (s *ResourceNotFoundException) StatusCode() int {
	return s.RespMetadata.StatusCode
}

// RequestID returns the service's response RequestID for request.
func (s *ResourceNotFoundException) RequestID() string {
	return s.RespMetadata.RequestID
}

// Code returns the exception type name.
func (s *ValidationException) Code() string {
	return "ValidationException"
}

// Message returns the exception's message.
func (s *ValidationException) Message() string {
	if s.Message_ != nil {
		return *s.Message_
	}
	return ""
}

// OrigErr always returns nil, satisfies awserr.Error interface.
func (s *ValidationException) OrigErr() error {
	return nil
}

func (s *ValidationException) Error() string {
	return fmt.Sprintf("%s: %s", s.Code(), s.Message())
}

// Status code returns the HTTP status code for the request's response error.
func (s *ValidationException) StatusCode() int {
	return s.RespMetadata.StatusCode
}

// RequestID returns the service's response RequestID for request.
func (s *ValidationException) RequestID() string {
	return s.RespMetadata.RequestID
}
