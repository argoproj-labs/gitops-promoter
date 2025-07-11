GitOps Promoter produces structured logs. All log messages emitted by controllers use the default controller-runtime log 
fields. Non-controller components (such as the webhook handler) use the same log format, but will not include 
controller-specific fields.

Additional standard fields for things like rate limiting logs will be added in the future and documented here.
