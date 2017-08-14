package model

import ()

type Transfer struct {
	Source Location
	Dest   Location
}

type Location struct {
	S3      *S3Location      `json:"s3,omitempty"`
	Cluster *ClusterLocation `json:"cluster,omitempty"`
}

type S3Location struct {
}

// ClusterLocation represents an OpenShift cluster we are archiving a project from, or unarchiving to.
type ClusterLocation struct {
	Namespace string
}
