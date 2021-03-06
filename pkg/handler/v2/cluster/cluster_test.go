/*
Copyright 2020 The Kubermatic Kubernetes Platform contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clusterv1alpha1 "github.com/kubermatic/machine-controller/pkg/apis/cluster/v1alpha1"
	apiv1 "k8c.io/kubermatic/v2/pkg/api/v1"
	kubermaticv1 "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1"
	"k8c.io/kubermatic/v2/pkg/handler/test"
	"k8c.io/kubermatic/v2/pkg/handler/test/hack"
	"k8c.io/kubermatic/v2/pkg/semver"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestCreateClusterEndpoint(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		Name                   string
		Body                   string
		ExpectedResponse       string
		HTTPStatus             int
		ProjectToSync          string
		ExistingProject        *kubermaticv1.Project
		ExistingAPIUser        *apiv1.User
		ExistingKubermaticObjs []runtime.Object
		RewriteClusterID       bool
	}{
		// scenario 1
		{
			Name:                   "scenario 1: a cluster with invalid spec is rejected",
			Body:                   `{"cluster":{"name":"keen-snyder","spec":{"cloud":{"fake":{"token":"dummy_token"},"dc":"fake-dc"}, "version":""}}}`,
			ExpectedResponse:       `{"error":{"code":400,"message":"invalid cluster: invalid cloud spec \"Version\" is required but was not specified"}}`,
			HTTPStatus:             http.StatusBadRequest,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(),
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
		// scenario 2
		{
			Name:             "scenario 2: cluster is created when valid spec and ssh key are passed",
			Body:             `{"cluster":{"name":"keen-snyder","spec":{"version":"1.15.0","cloud":{"fake":{"token":"dummy_token"},"dc":"fake-dc"}}}}`,
			ExpectedResponse: `{"id":"%s","name":"keen-snyder","creationTimestamp":"0001-01-01T00:00:00Z","type":"kubernetes","spec":{"cloud":{"dc":"fake-dc","fake":{}},"version":"1.15.0","oidc":{}},"status":{"version":"1.15.0","url":""}}`,
			RewriteClusterID: true,
			HTTPStatus:       http.StatusCreated,
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				// add an ssh key
				&kubermaticv1.UserSSHKey{
					ObjectMeta: metav1.ObjectMeta{
						Name: "key-c08aa5c7abf34504f18552846485267d-yafn",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "kubermatic.k8s.io/v1",
								Kind:       "Project",
								UID:        "",
								Name:       test.GenDefaultProject().Name,
							},
						},
					},
				},
			),
			ExistingAPIUser: test.GenDefaultAPIUser(),
		},
		// scenario 3
		{
			Name:                   "scenario 3: unable to create a cluster when the user doesn't belong to the project",
			Body:                   `{"cluster":{"humanReadableName":"keen-snyder","pause":false,"spec":{"version":"1.15.0","cloud":{"version":"1.15.0","fake":{},"dc":"fake-dc"}}},"sshKeys":["key-c08aa5c7abf34504f18552846485267d-yafn"]}`,
			ExpectedResponse:       `{"error":{"code":403,"message":"forbidden: \"john@acme.com\" doesn't belong to the given project = my-first-project-ID"}}`,
			HTTPStatus:             http.StatusForbidden,
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenUser("", "John", "john@acme.com")),
			ExistingAPIUser: func() *apiv1.User {
				defaultUser := test.GenDefaultAPIUser()
				defaultUser.Email = "john@acme.com"
				return defaultUser
			}(),
		},
		// scenario 4
		{
			Name:             "scenario 4: unable to create a cluster when project is not ready",
			Body:             `{"cluster":{"humanReadableName":"keen-snyder","pause":false,"spec":{"version":"1.15.0","cloud":{"fake":{},"dc":"fake-dc"}}},"sshKeys":["key-c08aa5c7abf34504f18552846485267d-yafn"]}`,
			ExpectedResponse: `{"error":{"code":503,"message":"Project is not initialized yet"}}`,
			HTTPStatus:       http.StatusServiceUnavailable,
			ExistingProject: func() *kubermaticv1.Project {
				project := test.GenDefaultProject()
				project.Status.Phase = kubermaticv1.ProjectInactive
				return project
			}(),
			ProjectToSync: test.GenDefaultProject().Name,
			ExistingKubermaticObjs: []runtime.Object{
				test.GenDefaultUser(),
				test.GenDefaultOwnerBinding(),
			},
			ExistingAPIUser: test.GenDefaultAPIUser(),
		},
		// scenario 5
		{
			Name:                   "scenario 5: openShift cluster is created",
			Body:                   `{"cluster":{"name":"keen-snyder","type":"openshift","spec":{"version":"4.1.0","openshift":{"imagePullSecret": "some-secret"},"cloud":{"fake":{"token":"dummy_token"},"dc":"fake-dc"}}}}`,
			ExpectedResponse:       `{"id":"%s","name":"keen-snyder","creationTimestamp":"0001-01-01T00:00:00Z","type":"openshift","spec":{"cloud":{"dc":"fake-dc","fake":{}},"version":"4.1.0","oidc":{}},"status":{"version":"4.1.0","url":""}}`,
			RewriteClusterID:       true,
			HTTPStatus:             http.StatusCreated,
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultSettings()),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
		// scenario 6
		{
			Name:                   "scenario 6: openShift cluster is created with existing custom credential",
			Body:                   `{"cluster":{"name":"keen-snyder","type":"openshift","credential":"fake","spec":{"version":"4.1.0","openshift":{"imagePullSecret": "some-secret"},"cloud":{"fake":{},"dc":"fake-dc"}}}}`,
			ExpectedResponse:       `{"id":"%s","name":"keen-snyder","creationTimestamp":"0001-01-01T00:00:00Z","type":"openshift","spec":{"cloud":{"dc":"fake-dc","fake":{}},"version":"4.1.0","oidc":{}},"status":{"version":"4.1.0","url":""}}`,
			RewriteClusterID:       true,
			HTTPStatus:             http.StatusCreated,
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultSettings()),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
		// scenario 7
		{
			Name:                   "scenario 7: custom credential doesn't exist for Fake cloud provider",
			Body:                   `{"cluster":{"name":"keen-snyder","type":"openshift","credential":"default","spec":{"version":"4.1.0","cloud":{"fake":{},"dc":"fake-dc"}}}}`,
			ExpectedResponse:       `{"error":{"code":400,"message":"invalid credentials: missing preset 'default' for the user 'bob@acme.com'"}}`,
			HTTPStatus:             http.StatusBadRequest,
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultSettings()),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
		{
			Name:                   "scenario 8: openShift cluster creation fails without imagePullSecret",
			Body:                   `{"cluster":{"name":"keen-snyder","type":"openshift","credential":"fake","spec":{"version":"4.1.0","cloud":{"fake":{},"dc":"fake-dc"}}}}`,
			ExpectedResponse:       `{"error":{"code":400,"message":"openshift clusters must be configured with an imagePullSecret"}}`,
			HTTPStatus:             http.StatusBadRequest,
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultSettings()),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
		{
			Name:                   "scenario 9a: rejected an attempt to create a cluster in email-restricted datacenter - legacy single domain restriction with requiredEmailDomains",
			Body:                   `{"cluster":{"name":"keen-snyder","spec":{"version":"1.15.0","cloud":{"fake":{"token":"dummy_token"},"dc":"restricted-fake-dc"}}}}`,
			ExpectedResponse:       `{"error":{"code":404,"message":"datacenter \"restricted-fake-dc\" not found"}}`,
			RewriteClusterID:       false,
			HTTPStatus:             http.StatusNotFound,
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
		{
			Name:                   "scenario 9b: rejected an attempt to create a cluster in email-restricted datacenter - domain array restriction with `requiredEmailDomains`",
			Body:                   `{"cluster":{"name":"keen-snyder","spec":{"version":"1.15.0","cloud":{"fake":{"token":"dummy_token"},"dc":"restricted-fake-dc2"}}}}`,
			ExpectedResponse:       `{"error":{"code":404,"message":"datacenter \"restricted-fake-dc2\" not found"}}`,
			RewriteClusterID:       false,
			HTTPStatus:             http.StatusNotFound,
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
		{
			Name:             "scenario 10a: create a cluster in email-restricted datacenter, to which the user does have access - legacy single domain restriction with requiredEmailDomains",
			Body:             `{"cluster":{"name":"keen-snyder","spec":{"version":"1.15.0","cloud":{"fake":{"token":"dummy_token"},"dc":"restricted-fake-dc"}}}}`,
			ExpectedResponse: `{"id":"%s","name":"keen-snyder","creationTimestamp":"0001-01-01T00:00:00Z","type":"kubernetes","spec":{"cloud":{"dc":"restricted-fake-dc","fake":{}},"version":"1.15.0","oidc":{}},"status":{"version":"1.15.0","url":""}}`,
			RewriteClusterID: true,
			HTTPStatus:       http.StatusCreated,
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				test.GenUser(test.UserID2, test.UserName2, test.UserEmail2),
				test.GenBinding(test.GenDefaultProject().Name, test.UserEmail2, "editors"),
			),
			ExistingAPIUser: test.GenAPIUser(test.UserName2, test.UserEmail2),
		},
		{
			Name:             "scenario 10b: create a cluster in email-restricted datacenter, to which the user does have access - domain array restriction with `requiredEmailDomains`",
			Body:             `{"cluster":{"name":"keen-snyder","spec":{"version":"1.15.0","cloud":{"fake":{"token":"dummy_token"},"dc":"restricted-fake-dc2"}}}}`,
			ExpectedResponse: `{"id":"%s","name":"keen-snyder","creationTimestamp":"0001-01-01T00:00:00Z","type":"kubernetes","spec":{"cloud":{"dc":"restricted-fake-dc2","fake":{}},"version":"1.15.0","oidc":{}},"status":{"version":"1.15.0","url":""}}`,
			RewriteClusterID: true,
			HTTPStatus:       http.StatusCreated,
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				test.GenUser(test.UserID2, test.UserName2, test.UserEmail2),
				test.GenBinding(test.GenDefaultProject().Name, test.UserEmail2, "editors"),
			),
			ExistingAPIUser: test.GenAPIUser(test.UserName2, test.UserEmail2),
		},
		{
			Name:             "scenario 11: create a cluster in audit-logging-enforced datacenter, without explicitly enabling audit logging",
			Body:             `{"cluster":{"name":"keen-snyder","spec":{"version":"1.15.0","cloud":{"fake":{"token":"dummy_token"},"dc":"audited-dc"}}}}`,
			ExpectedResponse: `{"id":"%s","name":"keen-snyder","creationTimestamp":"0001-01-01T00:00:00Z","type":"kubernetes","spec":{"cloud":{"dc":"audited-dc","fake":{}},"version":"1.15.0","oidc":{},"auditLogging":{"enabled":true}},"status":{"version":"1.15.0","url":""}}`,
			RewriteClusterID: true,
			HTTPStatus:       http.StatusCreated,
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				test.GenUser(test.UserID2, test.UserName2, test.UserEmail2),
				test.GenBinding(test.GenDefaultProject().Name, test.UserEmail2, "editors"),
			),
			ExistingAPIUser: test.GenAPIUser(test.UserName2, test.UserEmail2),
		},
		{
			Name:             "scenario 12: the admin user can create cluster for any project",
			Body:             `{"cluster":{"name":"keen-snyder","spec":{"version":"1.15.0","cloud":{"fake":{"token":"dummy_token"},"dc":"fake-dc"}}}}`,
			ExpectedResponse: `{"id":"%s","name":"keen-snyder","creationTimestamp":"0001-01-01T00:00:00Z","type":"kubernetes","spec":{"cloud":{"dc":"fake-dc","fake":{}},"version":"1.15.0","oidc":{}},"status":{"version":"1.15.0","url":""}}`,
			RewriteClusterID: true,
			HTTPStatus:       http.StatusCreated,
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				// add admin user
				genUser("John", "john@acme.com", true),
				// add an ssh key
				&kubermaticv1.UserSSHKey{
					ObjectMeta: metav1.ObjectMeta{
						Name: "key-c08aa5c7abf34504f18552846485267d-yafn",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "kubermatic.k8s.io/v1",
								Kind:       "Project",
								UID:        "",
								Name:       test.GenDefaultProject().Name,
							},
						},
					},
				},
			),
			ExistingAPIUser: test.GenAPIUser("John", "john@acme.com"),
		},
		// scenario 13
		{
			Name:                   "scenario 13: a cluster with invalid version",
			Body:                   `{"cluster":{"name":"keen-snyder","spec":{"cloud":{"fake":{"token":"dummy_token"},"dc":"fake-dc"}, "version":"1.2.3"}}}`,
			ExpectedResponse:       `{"error":{"code":400,"message":"invalid cluster: invalid cloud spec: unsupported version 1.2.3"}}`,
			HTTPStatus:             http.StatusBadRequest,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(),
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
		// scenario 14
		{
			Name:                   "scenario 14: a cluster without version",
			Body:                   `{"cluster":{"name":"keen-snyder","spec":{"cloud":{"fake":{"token":"dummy_token"},"dc":"fake-dc"}}}}`,
			ExpectedResponse:       `{"error":{"code":400,"message":"invalid cluster: invalid cloud spec \"Version\" is required but was not specified"}}`,
			HTTPStatus:             http.StatusBadRequest,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(),
			ProjectToSync:          test.GenDefaultProject().Name,
			ExistingAPIUser:        test.GenDefaultAPIUser(),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			req := httptest.NewRequest("POST", fmt.Sprintf("/api/v2/projects/%s/clusters", tc.ProjectToSync), strings.NewReader(tc.Body))
			res := httptest.NewRecorder()
			var kubermaticObj []runtime.Object
			if tc.ExistingProject != nil {
				kubermaticObj = append(kubermaticObj, tc.ExistingProject)
			}
			kubermaticObj = append(kubermaticObj, tc.ExistingKubermaticObjs...)

			ep, err := test.CreateTestEndpoint(*tc.ExistingAPIUser, []runtime.Object{}, kubermaticObj, test.GenDefaultVersions(), nil, hack.NewTestRouting)
			if err != nil {
				t.Fatalf("failed to create test endpoint due to %v", err)
			}

			ep.ServeHTTP(res, req)

			if res.Code != tc.HTTPStatus {
				t.Fatalf("Expected HTTP status code %d, got %d: %s", tc.HTTPStatus, res.Code, res.Body.String())
			}

			expectedResponse := tc.ExpectedResponse
			// since Cluster.Name is automatically generated by the system just rewrite it.
			if tc.RewriteClusterID {
				actualCluster := &apiv1.Cluster{}
				err = json.Unmarshal(res.Body.Bytes(), actualCluster)
				if err != nil {
					t.Fatal(err)
				}
				expectedResponse = fmt.Sprintf(tc.ExpectedResponse, actualCluster.ID)
			}

			test.CompareWithResult(t, res, expectedResponse)
		})
	}
}

func TestListClusters(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		Name                   string
		ExpectedClusters       []apiv1.Cluster
		HTTPStatus             int
		ExistingAPIUser        *apiv1.User
		ExistingKubermaticObjs []runtime.Object
	}{
		// scenario 1
		{
			Name: "scenario 1: list clusters that belong to the given project",
			ExpectedClusters: []apiv1.Cluster{
				{
					ObjectMeta: apiv1.ObjectMeta{
						ID:                "clusterAbcID",
						Name:              "clusterAbc",
						CreationTimestamp: apiv1.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC),
					},
					Spec: apiv1.ClusterSpec{
						Cloud: kubermaticv1.CloudSpec{
							DatacenterName: "FakeDatacenter",
							Fake:           &kubermaticv1.FakeCloudSpec{},
						},
						Version: *semver.NewSemverOrDie("9.9.9"),
					},
					Status: apiv1.ClusterStatus{
						Version: *semver.NewSemverOrDie("9.9.9"),
						URL:     "https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885",
					},
					Type: "kubernetes",
				},
				{
					ObjectMeta: apiv1.ObjectMeta{
						ID:                "clusterDefID",
						Name:              "clusterDef",
						CreationTimestamp: apiv1.Date(2013, 02, 04, 01, 54, 0, 0, time.UTC),
					},
					Spec: apiv1.ClusterSpec{
						Cloud: kubermaticv1.CloudSpec{
							DatacenterName: "FakeDatacenter",
							Fake:           &kubermaticv1.FakeCloudSpec{},
						},
						Version: *semver.NewSemverOrDie("9.9.9"),
					},
					Status: apiv1.ClusterStatus{
						Version: *semver.NewSemverOrDie("9.9.9"),
						URL:     "https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885",
					},
					Type: "kubernetes",
				},
				{
					ObjectMeta: apiv1.ObjectMeta{
						ID:                "clusterOpenstackID",
						Name:              "clusterOpenstack",
						CreationTimestamp: apiv1.Date(2013, 02, 04, 03, 54, 0, 0, time.UTC),
					},
					Spec: apiv1.ClusterSpec{
						Cloud: kubermaticv1.CloudSpec{
							DatacenterName: "OpenstackDatacenter",
							Openstack: &kubermaticv1.OpenstackCloudSpec{
								FloatingIPPool: "floatingIPPool",
								SubnetID:       "subnetID",
								Domain:         "domain",
								Network:        "network",
								RouterID:       "routerID",
								SecurityGroups: "securityGroups",
								Tenant:         "tenant",
							},
						},
						Version: *semver.NewSemverOrDie("9.9.9"),
					},
					Status: apiv1.ClusterStatus{
						Version: *semver.NewSemverOrDie("9.9.9"),
						URL:     "https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885",
					},
					Type: "kubernetes",
				},
			},
			HTTPStatus: http.StatusOK,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				test.GenCluster("clusterAbcID", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC)),
				test.GenCluster("clusterDefID", "clusterDef", test.GenDefaultProject().Name, time.Date(2013, 02, 04, 01, 54, 0, 0, time.UTC)),
				test.GenClusterWithOpenstack(test.GenCluster("clusterOpenstackID", "clusterOpenstack", test.GenDefaultProject().Name, time.Date(2013, 02, 04, 03, 54, 0, 0, time.UTC))),
			),
			ExistingAPIUser: test.GenDefaultAPIUser(),
		},
		// scenario 2
		{
			Name: "scenario 2: the admin John can list Bob's clusters",
			ExpectedClusters: []apiv1.Cluster{
				{
					ObjectMeta: apiv1.ObjectMeta{
						ID:                "clusterAbcID",
						Name:              "clusterAbc",
						CreationTimestamp: apiv1.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC),
					},
					Spec: apiv1.ClusterSpec{
						Cloud: kubermaticv1.CloudSpec{
							DatacenterName: "FakeDatacenter",
							Fake:           &kubermaticv1.FakeCloudSpec{},
						},
						Version: *semver.NewSemverOrDie("9.9.9"),
					},
					Status: apiv1.ClusterStatus{
						Version: *semver.NewSemverOrDie("9.9.9"),
						URL:     "https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885",
					},
					Type: "kubernetes",
				},
				{
					ObjectMeta: apiv1.ObjectMeta{
						ID:                "clusterDefID",
						Name:              "clusterDef",
						CreationTimestamp: apiv1.Date(2013, 02, 04, 01, 54, 0, 0, time.UTC),
					},
					Spec: apiv1.ClusterSpec{
						Cloud: kubermaticv1.CloudSpec{
							DatacenterName: "FakeDatacenter",
							Fake:           &kubermaticv1.FakeCloudSpec{},
						},
						Version: *semver.NewSemverOrDie("9.9.9"),
					},
					Status: apiv1.ClusterStatus{
						Version: *semver.NewSemverOrDie("9.9.9"),
						URL:     "https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885",
					},
					Type: "kubernetes",
				},
				{
					ObjectMeta: apiv1.ObjectMeta{
						ID:                "clusterOpenstackID",
						Name:              "clusterOpenstack",
						CreationTimestamp: apiv1.Date(2013, 02, 04, 03, 54, 0, 0, time.UTC),
					},
					Spec: apiv1.ClusterSpec{
						Cloud: kubermaticv1.CloudSpec{
							DatacenterName: "OpenstackDatacenter",
							Openstack: &kubermaticv1.OpenstackCloudSpec{
								FloatingIPPool: "floatingIPPool",
								SubnetID:       "subnetID",
								Domain:         "domain",
								Network:        "network",
								RouterID:       "routerID",
								SecurityGroups: "securityGroups",
								Tenant:         "tenant",
							},
						},
						Version: *semver.NewSemverOrDie("9.9.9"),
					},
					Status: apiv1.ClusterStatus{
						Version: *semver.NewSemverOrDie("9.9.9"),
						URL:     "https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885",
					},
					Type: "kubernetes",
				},
			},
			HTTPStatus: http.StatusOK,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				genUser("John", "john@acme.com", true),
				test.GenCluster("clusterAbcID", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC)),
				test.GenCluster("clusterDefID", "clusterDef", test.GenDefaultProject().Name, time.Date(2013, 02, 04, 01, 54, 0, 0, time.UTC)),
				test.GenClusterWithOpenstack(test.GenCluster("clusterOpenstackID", "clusterOpenstack", test.GenDefaultProject().Name, time.Date(2013, 02, 04, 03, 54, 0, 0, time.UTC))),
			),
			ExistingAPIUser: test.GenAPIUser("John", "john@acme.com"),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			req := httptest.NewRequest("GET", fmt.Sprintf("/api/v2/projects/%s/clusters", test.ProjectName), strings.NewReader(""))
			res := httptest.NewRecorder()
			var kubermaticObj []runtime.Object
			kubermaticObj = append(kubermaticObj, tc.ExistingKubermaticObjs...)
			ep, err := test.CreateTestEndpoint(*tc.ExistingAPIUser, []runtime.Object{}, kubermaticObj, nil, nil, hack.NewTestRouting)
			if err != nil {
				t.Fatalf("failed to create test endpoint due to %v", err)
			}

			ep.ServeHTTP(res, req)

			if res.Code != tc.HTTPStatus {
				t.Fatalf("Expected HTTP status code %d, got %d: %s", tc.HTTPStatus, res.Code, res.Body.String())
			}

			actualClusters := test.NewClusterV1SliceWrapper{}
			actualClusters.DecodeOrDie(res.Body, t).Sort()

			wrappedExpectedClusters := test.NewClusterV1SliceWrapper(tc.ExpectedClusters)
			wrappedExpectedClusters.Sort()

			actualClusters.EqualOrDie(wrappedExpectedClusters, t)
		})
	}
}

func TestGetCluster(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		Name                   string
		Body                   string
		ExpectedResponse       string
		HTTPStatus             int
		ClusterToGet           string
		ExistingAPIUser        *apiv1.User
		ExistingKubermaticObjs []runtime.Object
	}{
		// scenario 1
		{
			Name:             "scenario 1: gets cluster with the given name that belongs to the given project",
			Body:             ``,
			ExpectedResponse: `{"id":"defClusterID","name":"defClusterName","creationTimestamp":"2013-02-03T19:54:00Z","type":"kubernetes","spec":{"cloud":{"dc":"FakeDatacenter","fake":{}},"version":"9.9.9","oidc":{}},"status":{"version":"9.9.9","url":"https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885"}}`,
			ClusterToGet:     test.GenDefaultCluster().Name,
			HTTPStatus:       http.StatusOK,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				test.GenDefaultCluster(),
				test.GenCluster("clusterAbcID", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC)),
			),
			ExistingAPIUser: test.GenDefaultAPIUser(),
		},
		// scenario 2
		{
			Name:             "scenario 2: gets cluster for Openstack and no sensitive data (credentials) are returned",
			Body:             ``,
			ExpectedResponse: `{"id":"defClusterID","name":"defClusterName","creationTimestamp":"2013-02-03T19:54:00Z","type":"kubernetes","spec":{"cloud":{"dc":"OpenstackDatacenter","openstack":{"floatingIpPool":"floatingIPPool","tenant":"tenant","domain":"domain","network":"network","securityGroups":"securityGroups","routerID":"routerID","subnetID":"subnetID"}},"version":"9.9.9","oidc":{}},"status":{"version":"9.9.9","url":"https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885"}}`,
			ClusterToGet:     test.GenDefaultCluster().Name,
			HTTPStatus:       http.StatusOK,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				test.GenClusterWithOpenstack(test.GenDefaultCluster()),
				test.GenCluster("clusterAbcID", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC)),
			),
			ExistingAPIUser: test.GenDefaultAPIUser(),
		},
		// scenario 3
		{
			Name:             "scenario 3: the admin John can get Bob's cluster",
			Body:             ``,
			ExpectedResponse: `{"id":"defClusterID","name":"defClusterName","creationTimestamp":"2013-02-03T19:54:00Z","type":"kubernetes","spec":{"cloud":{"dc":"OpenstackDatacenter","openstack":{"floatingIpPool":"floatingIPPool","tenant":"tenant","domain":"domain","network":"network","securityGroups":"securityGroups","routerID":"routerID","subnetID":"subnetID"}},"version":"9.9.9","oidc":{}},"status":{"version":"9.9.9","url":"https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885"}}`,
			ClusterToGet:     test.GenDefaultCluster().Name,
			HTTPStatus:       http.StatusOK,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				genUser("John", "john@acme.com", true),
				test.GenClusterWithOpenstack(test.GenDefaultCluster()),
				test.GenCluster("clusterAbcID", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC)),
			),
			ExistingAPIUser: test.GenAPIUser("John", "john@acme.com"),
		},
		// scenario 4
		{
			Name:             "scenario 4: the regular user John can not get Bob's cluster",
			Body:             ``,
			ExpectedResponse: `{"error":{"code":403,"message":"forbidden: \"john@acme.com\" doesn't belong to the given project = my-first-project-ID"}}`,
			ClusterToGet:     test.GenDefaultCluster().Name,
			HTTPStatus:       http.StatusForbidden,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				genUser("John", "john@acme.com", false),
				test.GenClusterWithOpenstack(test.GenDefaultCluster()),
				test.GenCluster("clusterAbcID", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC)),
			),
			ExistingAPIUser: test.GenAPIUser("John", "john@acme.com"),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			req := httptest.NewRequest("GET", fmt.Sprintf("/api/v2/projects/%s/clusters/%s", test.ProjectName, tc.ClusterToGet), strings.NewReader(tc.Body))
			res := httptest.NewRecorder()
			var kubermaticObj []runtime.Object
			kubermaticObj = append(kubermaticObj, tc.ExistingKubermaticObjs...)
			ep, err := test.CreateTestEndpoint(*tc.ExistingAPIUser, []runtime.Object{}, kubermaticObj, nil, nil, hack.NewTestRouting)
			if err != nil {
				t.Fatalf("failed to create test endpoint due to %v", err)
			}

			ep.ServeHTTP(res, req)

			if res.Code != tc.HTTPStatus {
				t.Fatalf("Expected HTTP status code %d, got %d: %s", tc.HTTPStatus, res.Code, res.Body.String())
			}

			test.CompareWithResult(t, res, tc.ExpectedResponse)
		})
	}
}

func TestDeleteClusterEndpoint(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		Name                          string
		Body                          string
		ExpectedResponse              string
		HTTPStatus                    int
		ProjectToSync                 string
		ClusterToSync                 string
		ExistingKubermaticObjs        []runtime.Object
		ExistingAPIUser               *apiv1.User
		ExpectedListClusterKeysStatus int
	}{
		{
			Name:             "scenario 1: tests deletion of a cluster and its dependant resources",
			Body:             ``,
			ExpectedResponse: `{}`,
			HTTPStatus:       http.StatusOK,
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				// add a cluster
				test.GenCluster("clusterAbcID", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC)),
				// add ssh keys
				&kubermaticv1.UserSSHKey{
					ObjectMeta: metav1.ObjectMeta{
						Name: "key-c08aa5c7abf34504f18552846485267d-yafn",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "kubermatic.k8s.io/v1",
								Kind:       "Project",
								UID:        "",
								Name:       test.GenDefaultProject().Name,
							},
						},
					},
					Spec: kubermaticv1.SSHKeySpec{
						Clusters: []string{"clusterAbcID"},
					},
				},
				&kubermaticv1.UserSSHKey{
					ObjectMeta: metav1.ObjectMeta{
						Name: "key-abc-yafn",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "kubermatic.k8s.io/v1",
								Kind:       "Project",
								UID:        "",
								Name:       test.GenDefaultProject().Name,
							},
						},
					},
					Spec: kubermaticv1.SSHKeySpec{
						Clusters: []string{"clusterAbcID"},
					},
				},
			),
			ClusterToSync:                 "clusterAbcID",
			ExistingAPIUser:               test.GenDefaultAPIUser(),
			ExpectedListClusterKeysStatus: http.StatusNotFound,
		},
		{
			Name:             "scenario 2: the admin John can delete Bob's cluster",
			Body:             ``,
			ExpectedResponse: `{}`,
			HTTPStatus:       http.StatusOK,
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				genUser("John", "john@acme.com", true),
				// add a cluster
				test.GenCluster("clusterAbcID", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC)),
				// add ssh keys
				&kubermaticv1.UserSSHKey{
					ObjectMeta: metav1.ObjectMeta{
						Name: "key-c08aa5c7abf34504f18552846485267d-yafn",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "kubermatic.k8s.io/v1",
								Kind:       "Project",
								UID:        "",
								Name:       test.GenDefaultProject().Name,
							},
						},
					},
					Spec: kubermaticv1.SSHKeySpec{
						Clusters: []string{"clusterAbcID"},
					},
				},
				&kubermaticv1.UserSSHKey{
					ObjectMeta: metav1.ObjectMeta{
						Name: "key-abc-yafn",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "kubermatic.k8s.io/v1",
								Kind:       "Project",
								UID:        "",
								Name:       test.GenDefaultProject().Name,
							},
						},
					},
					Spec: kubermaticv1.SSHKeySpec{
						Clusters: []string{"clusterAbcID"},
					},
				},
			),
			ClusterToSync:                 "clusterAbcID",
			ExistingAPIUser:               test.GenAPIUser("John", "john@acme.com"),
			ExpectedListClusterKeysStatus: http.StatusNotFound,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {

			// validate if deletion was successful
			req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v2/projects/%s/clusters/%s", tc.ProjectToSync, tc.ClusterToSync), strings.NewReader(tc.Body))
			res := httptest.NewRecorder()
			var kubermaticObj []runtime.Object
			kubermaticObj = append(kubermaticObj, tc.ExistingKubermaticObjs...)
			ep, err := test.CreateTestEndpoint(*tc.ExistingAPIUser, []runtime.Object{}, kubermaticObj, nil, nil, hack.NewTestRouting)
			if err != nil {
				t.Fatalf("failed to create test endpoint due to %v", err)
			}

			ep.ServeHTTP(res, req)

			if res.Code != tc.HTTPStatus {
				t.Fatalf("Expected HTTP status code %d, got %d: %s", tc.HTTPStatus, res.Code, res.Body.String())
			}
			test.CompareWithResult(t, res, tc.ExpectedResponse)

			// validate if the cluster was deleted
			req = httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%s/dc/us-central1/clusters/abcd/sshkeys", tc.ProjectToSync), strings.NewReader(tc.Body))
			res = httptest.NewRecorder()
			ep.ServeHTTP(res, req)
			if res.Code != tc.ExpectedListClusterKeysStatus {
				t.Fatalf("Expected HTTP status code %d, got %d: %s", tc.ExpectedListClusterKeysStatus, res.Code, res.Body.String())
			}
		})
	}
}

func TestPatchCluster(t *testing.T) {
	t.Parallel()

	const fakeDC = "fake-dc"
	cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
	cluster.Spec.Cloud.DatacenterName = "us-central1"

	testcases := []struct {
		Name                      string
		Body                      string
		ExpectedResponse          string
		HTTPStatus                int
		cluster                   string
		project                   string
		ExistingAPIUser           *apiv1.User
		ExistingMachines          []*clusterv1alpha1.Machine
		ExistingKubermaticObjects []runtime.Object
	}{
		// scenario 1
		{
			Name:             "scenario 1: update the cluster version",
			Body:             `{"spec":{"version":"1.2.3"}}`,
			ExpectedResponse: `{"id":"keen-snyder","name":"clusterAbc","creationTimestamp":"2013-02-03T19:54:00Z","type":"kubernetes","spec":{"cloud":{"dc":"fake-dc","fake":{}},"version":"1.2.3","oidc":{}},"status":{"version":"1.2.3","url":"https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885"}}`,
			cluster:          "keen-snyder",
			HTTPStatus:       http.StatusOK,
			project:          test.GenDefaultProject().Name,
			ExistingAPIUser:  test.GenDefaultAPIUser(),
			ExistingKubermaticObjects: test.GenDefaultKubermaticObjects(
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Spec.Cloud.DatacenterName = fakeDC
					return cluster
				}()),
		},
		// scenario 2
		{
			Name:                      "scenario 2: fail on invalid patch json",
			Body:                      `{"spec":{"cloud":{"dc":"dc1"`,
			ExpectedResponse:          `{"error":{"code":400,"message":"cannot patch cluster: Invalid JSON Patch"}}`,
			cluster:                   "keen-snyder",
			HTTPStatus:                http.StatusBadRequest,
			project:                   test.GenDefaultProject().Name,
			ExistingAPIUser:           test.GenDefaultAPIUser(),
			ExistingKubermaticObjects: test.GenDefaultKubermaticObjects(test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))),
		},
		// scenario 3
		{
			Name:             "scenario 3: tried to update cluser with older but compatible nodes",
			Body:             `{"spec":{"version":"9.11.3"}}`, // kubelet is 9.9.9, maximum compatible master is 9.11.x
			ExpectedResponse: `{"id":"keen-snyder","name":"clusterAbc","creationTimestamp":"2013-02-03T19:54:00Z","type":"kubernetes","spec":{"cloud":{"dc":"fake-dc","fake":{}},"version":"9.11.3","oidc":{}},"status":{"version":"9.11.3","url":"https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885"}}`,
			cluster:          "keen-snyder",
			HTTPStatus:       http.StatusOK,
			project:          test.GenDefaultProject().Name,
			ExistingAPIUser:  test.GenDefaultAPIUser(),
			ExistingKubermaticObjects: test.GenDefaultKubermaticObjects(
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Spec.Cloud.DatacenterName = fakeDC
					return cluster
				}(),
			),
			ExistingMachines: []*clusterv1alpha1.Machine{
				test.GenTestMachine("venus", `{"cloudProvider":"digitalocean","cloudProviderSpec":{"token":"dummy-token","region":"fra1","size":"2GB"},"operatingSystem":"ubuntu","containerRuntimeInfo":{"name":"docker","version":"1.13"},"operatingSystemSpec":{"distUpgradeOnBoot":true}}`, map[string]string{"md-id": "123", "some-other": "xyz"}, nil),
				test.GenTestMachine("mars", `{"cloudProvider":"aws","cloudProviderSpec":{"token":"dummy-token","region":"eu-central-1","availabilityZone":"eu-central-1a","vpcId":"vpc-819f62e9","subnetId":"subnet-2bff4f43","instanceType":"t2.micro","diskSize":50}, "containerRuntimeInfo":{"name":"docker","version":"1.12"},"operatingSystem":"ubuntu", "operatingSystemSpec":{"distUpgradeOnBoot":false}}`, map[string]string{"md-id": "123", "some-other": "xyz"}, nil),
			},
		},
		// scenario 4
		{
			Name:             "scenario 4: tried to update cluser with old nodes",
			Body:             `{"spec":{"version":"9.12.3"}}`, // kubelet is 9.9.9, maximum compatible master is 9.11.x
			ExpectedResponse: `{"error":{"code":400,"message":"Cluster contains nodes running the following incompatible kubelet versions: [9.9.9]. Upgrade your nodes before you upgrade the cluster."}}`,
			cluster:          "keen-snyder",
			HTTPStatus:       http.StatusBadRequest,
			project:          test.GenDefaultProject().Name,
			ExistingAPIUser:  test.GenDefaultAPIUser(),
			ExistingKubermaticObjects: test.GenDefaultKubermaticObjects(
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Spec.Cloud.DatacenterName = "us-central1"
					return cluster
				}(),
			),
			ExistingMachines: []*clusterv1alpha1.Machine{
				test.GenTestMachine("venus", `{"cloudProvider":"digitalocean","cloudProviderSpec":{"token":"dummy-token","region":"fra1","size":"2GB"},"operatingSystem":"ubuntu","containerRuntimeInfo":{"name":"docker","version":"1.13"},"operatingSystemSpec":{"distUpgradeOnBoot":true}}`, map[string]string{"md-id": "123", "some-other": "xyz"}, nil),
				test.GenTestMachine("mars", `{"cloudProvider":"aws","cloudProviderSpec":{"token":"dummy-token","region":"eu-central-1","availabilityZone":"eu-central-1a","vpcId":"vpc-819f62e9","subnetId":"subnet-2bff4f43","instanceType":"t2.micro","diskSize":50}, "containerRuntimeInfo":{"name":"docker","version":"1.12"},"operatingSystem":"ubuntu", "operatingSystemSpec":{"distUpgradeOnBoot":false}}`, map[string]string{"md-id": "123", "some-other": "xyz"}, nil),
			},
		},
		// scenario 5
		{
			Name:             "scenario 5: tried to downgrade cluser to version older than its nodes",
			Body:             `{"spec":{"version":"9.8.12"}}`, // kubelet is 9.9.9, cluster cannot be older
			ExpectedResponse: `{"error":{"code":400,"message":"Cluster contains nodes running the following incompatible kubelet versions: [9.9.9]. Upgrade your nodes before you upgrade the cluster."}}`,
			cluster:          "keen-snyder",
			HTTPStatus:       http.StatusBadRequest,
			project:          test.GenDefaultProject().Name,
			ExistingAPIUser:  test.GenDefaultAPIUser(),
			ExistingKubermaticObjects: test.GenDefaultKubermaticObjects(
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Spec.Cloud.DatacenterName = "us-central1"
					return cluster
				}(),
			),
			ExistingMachines: []*clusterv1alpha1.Machine{
				test.GenTestMachine("venus", `{"cloudProvider":"digitalocean","cloudProviderSpec":{"token":"dummy-token","region":"fra1","size":"2GB"},"operatingSystem":"ubuntu","containerRuntimeInfo":{"name":"docker","version":"1.13"},"operatingSystemSpec":{"distUpgradeOnBoot":true}}`, map[string]string{"md-id": "123", "some-other": "xyz"}, nil),
				test.GenTestMachine("mars", `{"cloudProvider":"aws","cloudProviderSpec":{"token":"dummy-token","region":"eu-central-1","availabilityZone":"eu-central-1a","vpcId":"vpc-819f62e9","subnetId":"subnet-2bff4f43","instanceType":"t2.micro","diskSize":50}, "containerRuntimeInfo":{"name":"docker","version":"1.12"},"operatingSystem":"ubuntu", "operatingSystemSpec":{"distUpgradeOnBoot":false}}`, map[string]string{"md-id": "123", "some-other": "xyz"}, nil),
			},
		},
		// scenario 6
		{
			Name:             "scenario 6: the admin John can update Bob's cluster version",
			Body:             `{"spec":{"version":"1.2.3"}}`,
			ExpectedResponse: `{"id":"keen-snyder","name":"clusterAbc","creationTimestamp":"2013-02-03T19:54:00Z","type":"kubernetes","spec":{"cloud":{"dc":"fake-dc","fake":{}},"version":"1.2.3","oidc":{}},"status":{"version":"1.2.3","url":"https://w225mx4z66.asia-east1-a-1.cloud.kubermatic.io:31885"}}`,
			cluster:          "keen-snyder",
			HTTPStatus:       http.StatusOK,
			project:          test.GenDefaultProject().Name,
			ExistingAPIUser:  test.GenAPIUser("John", "john@acme.com"),
			ExistingKubermaticObjects: test.GenDefaultKubermaticObjects(
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Spec.Cloud.DatacenterName = fakeDC
					return cluster
				}(), genUser("John", "john@acme.com", true)),
		},
		// scenario 7
		{
			Name:             "scenario 7: the regular user John can not update Bob's cluster version",
			Body:             `{"spec":{"version":"1.2.3"}}`,
			ExpectedResponse: `{"error":{"code":403,"message":"forbidden: \"john@acme.com\" doesn't belong to the given project = my-first-project-ID"}}`,
			cluster:          "keen-snyder",
			HTTPStatus:       http.StatusForbidden,
			project:          test.GenDefaultProject().Name,
			ExistingAPIUser:  test.GenAPIUser("John", "john@acme.com"),
			ExistingKubermaticObjects: test.GenDefaultKubermaticObjects(
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Spec.Cloud.DatacenterName = fakeDC
					return cluster
				}(), genUser("John", "john@acme.com", false)),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			var machineObj []runtime.Object
			for _, existingMachine := range tc.ExistingMachines {
				machineObj = append(machineObj, existingMachine)
			}
			// test data
			req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v2/projects/%s/clusters/%s", tc.project, tc.cluster), strings.NewReader(tc.Body))
			res := httptest.NewRecorder()
			ep, _, err := test.CreateTestEndpointAndGetClients(*tc.ExistingAPIUser, nil, []runtime.Object{}, machineObj, tc.ExistingKubermaticObjects, nil, nil, hack.NewTestRouting)
			if err != nil {
				t.Fatalf("failed to create test endpoint due to %v", err)
			}

			// act
			ep.ServeHTTP(res, req)

			// validate
			if res.Code != tc.HTTPStatus {
				t.Fatalf("Expected HTTP status code %d, got %d: %s", tc.HTTPStatus, res.Code, res.Body.String())
			}

			test.CompareWithResult(t, res, tc.ExpectedResponse)
		})
	}
}

func TestGetClusterEventsEndpoint(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		Name                   string
		HTTPStatus             int
		ExpectedResult         string
		ProjectIDToSync        string
		ClusterIDToSync        string
		ExistingProject        *kubermaticv1.Project
		ExistingKubermaticUser *kubermaticv1.User
		ExistingAPIUser        *apiv1.User
		ExistingKubermaticObjs []runtime.Object
		ExistingEvents         []*corev1.Event
		NodeDeploymentID       string
		QueryParams            string
	}{
		// scenario 1
		{
			Name:                   "scenario 1: list all events",
			HTTPStatus:             http.StatusOK,
			ClusterIDToSync:        test.GenDefaultCluster().Name,
			ProjectIDToSync:        test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultCluster()),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
			ExistingEvents: []*corev1.Event{
				test.GenTestEvent("event-1", corev1.EventTypeNormal, "Started", "message started", "Cluster", "venus-1-machine"),
				test.GenTestEvent("event-2", corev1.EventTypeWarning, "Killed", "message killed", "Cluster", "venus-1-machine"),
			},
			ExpectedResult: `[{"name":"event-1","creationTimestamp":"0001-01-01T00:00:00Z","message":"message started","type":"Normal","involvedObject":{"type":"Cluster","namespace":"kube-system","name":"testMachine"},"lastTimestamp":"0001-01-01T00:00:00Z","count":1},{"name":"event-2","creationTimestamp":"0001-01-01T00:00:00Z","message":"message killed","type":"Warning","involvedObject":{"type":"Cluster","namespace":"kube-system","name":"testMachine"},"lastTimestamp":"0001-01-01T00:00:00Z","count":1}]`,
		},
		// scenario 2
		{
			Name:                   "scenario 2: list all warning events",
			QueryParams:            "?type=warning",
			HTTPStatus:             http.StatusOK,
			ClusterIDToSync:        test.GenDefaultCluster().Name,
			ProjectIDToSync:        test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultCluster()),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
			ExistingEvents: []*corev1.Event{
				test.GenTestEvent("event-1", corev1.EventTypeNormal, "Started", "message started", "Cluster", "venus-1-machine"),
				test.GenTestEvent("event-2", corev1.EventTypeWarning, "Killed", "message killed", "Cluster", "venus-1-machine"),
			},
			ExpectedResult: `[{"name":"event-2","creationTimestamp":"0001-01-01T00:00:00Z","message":"message killed","type":"Warning","involvedObject":{"type":"Cluster","namespace":"kube-system","name":"testMachine"},"lastTimestamp":"0001-01-01T00:00:00Z","count":1}]`,
		},
		// scenario 3
		{
			Name:                   "scenario 3: list all normal events",
			QueryParams:            "?type=normal",
			HTTPStatus:             http.StatusOK,
			ClusterIDToSync:        test.GenDefaultCluster().Name,
			ProjectIDToSync:        test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultCluster()),
			ExistingAPIUser:        test.GenDefaultAPIUser(),
			ExistingEvents: []*corev1.Event{
				test.GenTestEvent("event-1", corev1.EventTypeNormal, "Started", "message started", "Cluster", "venus-1-machine"),
				test.GenTestEvent("event-2", corev1.EventTypeWarning, "Killed", "message killed", "Cluster", "venus-1-machine"),
			},
			ExpectedResult: `[{"name":"event-1","creationTimestamp":"0001-01-01T00:00:00Z","message":"message started","type":"Normal","involvedObject":{"type":"Cluster","namespace":"kube-system","name":"testMachine"},"lastTimestamp":"0001-01-01T00:00:00Z","count":1}]`,
		},
		// scenario 4
		{
			Name:                   "scenario 4: the admin John can list Bob's cluster events",
			HTTPStatus:             http.StatusOK,
			ClusterIDToSync:        test.GenDefaultCluster().Name,
			ProjectIDToSync:        test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultCluster(), genUser("John", "john@acme.com", true)),
			ExistingAPIUser:        test.GenAPIUser("John", "john@acme.com"),
			ExistingEvents: []*corev1.Event{
				test.GenTestEvent("event-1", corev1.EventTypeNormal, "Started", "message started", "Cluster", "venus-1-machine"),
				test.GenTestEvent("event-2", corev1.EventTypeWarning, "Killed", "message killed", "Cluster", "venus-1-machine"),
			},
			ExpectedResult: `[{"name":"event-1","creationTimestamp":"0001-01-01T00:00:00Z","message":"message started","type":"Normal","involvedObject":{"type":"Cluster","namespace":"kube-system","name":"testMachine"},"lastTimestamp":"0001-01-01T00:00:00Z","count":1},{"name":"event-2","creationTimestamp":"0001-01-01T00:00:00Z","message":"message killed","type":"Warning","involvedObject":{"type":"Cluster","namespace":"kube-system","name":"testMachine"},"lastTimestamp":"0001-01-01T00:00:00Z","count":1}]`,
		},
		// scenario 5
		{
			Name:                   "scenario 5: the user John can not list Bob's cluster events",
			HTTPStatus:             http.StatusForbidden,
			ClusterIDToSync:        test.GenDefaultCluster().Name,
			ProjectIDToSync:        test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(test.GenDefaultCluster(), genUser("John", "john@acme.com", false)),
			ExistingAPIUser:        test.GenAPIUser("John", "john@acme.com"),
			ExistingEvents: []*corev1.Event{
				test.GenTestEvent("event-1", corev1.EventTypeNormal, "Started", "message started", "Cluster", "venus-1-machine"),
				test.GenTestEvent("event-2", corev1.EventTypeWarning, "Killed", "message killed", "Cluster", "venus-1-machine"),
			},
			ExpectedResult: `{"error":{"code":403,"message":"forbidden: \"john@acme.com\" doesn't belong to the given project = my-first-project-ID"}}`,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			req := httptest.NewRequest("GET", fmt.Sprintf("/api/v2/projects/%s/clusters/%s/events%s", tc.ProjectIDToSync, tc.ClusterIDToSync, tc.QueryParams), strings.NewReader(""))
			res := httptest.NewRecorder()
			kubermaticObj := make([]runtime.Object, 0)
			machineObj := make([]runtime.Object, 0)
			kubernetesObj := make([]runtime.Object, 0)
			for _, existingEvents := range tc.ExistingEvents {
				kubernetesObj = append(kubernetesObj, existingEvents)
			}
			kubermaticObj = append(kubermaticObj, tc.ExistingKubermaticObjs...)

			ep, _, err := test.CreateTestEndpointAndGetClients(*tc.ExistingAPIUser, nil, kubernetesObj, machineObj, kubermaticObj, nil, nil, hack.NewTestRouting)
			if err != nil {
				t.Fatalf("failed to create test endpoint due to %v", err)
			}

			ep.ServeHTTP(res, req)

			if res.Code != tc.HTTPStatus {
				t.Fatalf("Expected HTTP status code %d, got %d: %s", tc.HTTPStatus, res.Code, res.Body.String())
			}

			test.CompareWithResult(t, res, tc.ExpectedResult)
		})
	}
}

func TestGetClusterHealth(t *testing.T) {
	t.Parallel()
	testcases := []struct {
		Name                   string
		Body                   string
		ExpectedResponse       string
		HTTPStatus             int
		ClusterToGet           string
		ProjectToSync          string
		ExistingAPIUser        *apiv1.User
		ExistingKubermaticObjs []runtime.Object
	}{
		// scenario 1
		{
			Name:             "scenario 1: get existing cluster health status",
			Body:             ``,
			ExpectedResponse: `{"apiserver":1,"scheduler":0,"controller":1,"machineController":0,"etcd":1,"cloudProviderInfrastructure":1,"userClusterControllerManager":1}`,
			HTTPStatus:       http.StatusOK,
			ClusterToGet:     "keen-snyder",
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				// add a cluster
				test.GenCluster("clusterDefID", "clusterDef", test.GenDefaultProject().Name, time.Date(2013, 02, 04, 01, 54, 0, 0, time.UTC)),
				// add another cluster
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Status.ExtendedHealth = kubermaticv1.ExtendedClusterHealth{

						Apiserver:                    kubermaticv1.HealthStatusUp,
						Scheduler:                    kubermaticv1.HealthStatusDown,
						Controller:                   kubermaticv1.HealthStatusUp,
						MachineController:            kubermaticv1.HealthStatusDown,
						Etcd:                         kubermaticv1.HealthStatusUp,
						CloudProviderInfrastructure:  kubermaticv1.HealthStatusUp,
						UserClusterControllerManager: kubermaticv1.HealthStatusUp,
					}
					return cluster
				}(),
			),
			ExistingAPIUser: test.GenDefaultAPIUser(),
		},
		// scenario 2
		{
			Name:             "scenario 2: the admin Bob can get John's cluster health status",
			Body:             ``,
			ExpectedResponse: `{"apiserver":1,"scheduler":0,"controller":1,"machineController":0,"etcd":1,"cloudProviderInfrastructure":1,"userClusterControllerManager":1}`,
			HTTPStatus:       http.StatusOK,
			ClusterToGet:     "keen-snyder",
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				// add admin user
				genUser("John", "john@acme.com", true),
				// add a cluster
				test.GenCluster("clusterDefID", "clusterDef", test.GenDefaultProject().Name, time.Date(2013, 02, 04, 01, 54, 0, 0, time.UTC)),
				// add another cluster
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Status.ExtendedHealth = kubermaticv1.ExtendedClusterHealth{

						Apiserver:                    kubermaticv1.HealthStatusUp,
						Scheduler:                    kubermaticv1.HealthStatusDown,
						Controller:                   kubermaticv1.HealthStatusUp,
						MachineController:            kubermaticv1.HealthStatusDown,
						Etcd:                         kubermaticv1.HealthStatusUp,
						CloudProviderInfrastructure:  kubermaticv1.HealthStatusUp,
						UserClusterControllerManager: kubermaticv1.HealthStatusUp,
					}
					return cluster
				}(),
			),
			ExistingAPIUser: test.GenAPIUser("John", "john@acme.com"),
		},
		// scenario 3
		{
			Name:             "scenario 3: the user Bob can not get John's cluster health status",
			Body:             ``,
			ExpectedResponse: `{"error":{"code":403,"message":"forbidden: \"john@acme.com\" doesn't belong to the given project = my-first-project-ID"}}`,
			HTTPStatus:       http.StatusForbidden,
			ClusterToGet:     "keen-snyder",
			ProjectToSync:    test.GenDefaultProject().Name,
			ExistingKubermaticObjs: test.GenDefaultKubermaticObjects(
				// add regular user John
				genUser("John", "john@acme.com", false),
				// add a cluster
				test.GenCluster("clusterDefID", "clusterDef", test.GenDefaultProject().Name, time.Date(2013, 02, 04, 01, 54, 0, 0, time.UTC)),
				// add another cluster
				func() *kubermaticv1.Cluster {
					cluster := test.GenCluster("keen-snyder", "clusterAbc", test.GenDefaultProject().Name, time.Date(2013, 02, 03, 19, 54, 0, 0, time.UTC))
					cluster.Status.ExtendedHealth = kubermaticv1.ExtendedClusterHealth{

						Apiserver:                    kubermaticv1.HealthStatusUp,
						Scheduler:                    kubermaticv1.HealthStatusDown,
						Controller:                   kubermaticv1.HealthStatusUp,
						MachineController:            kubermaticv1.HealthStatusDown,
						Etcd:                         kubermaticv1.HealthStatusUp,
						CloudProviderInfrastructure:  kubermaticv1.HealthStatusUp,
						UserClusterControllerManager: kubermaticv1.HealthStatusUp,
					}
					return cluster
				}(),
			),
			ExistingAPIUser: test.GenAPIUser("John", "john@acme.com"),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			req := httptest.NewRequest("GET", fmt.Sprintf("/api/v2/projects/%s/clusters/%s/health", tc.ProjectToSync, tc.ClusterToGet), strings.NewReader(tc.Body))
			res := httptest.NewRecorder()
			var kubermaticObj []runtime.Object
			kubermaticObj = append(kubermaticObj, tc.ExistingKubermaticObjs...)
			ep, err := test.CreateTestEndpoint(*tc.ExistingAPIUser, []runtime.Object{}, kubermaticObj, nil, nil, hack.NewTestRouting)
			if err != nil {
				t.Fatalf("failed to create test endpoint due to %v", err)
			}

			ep.ServeHTTP(res, req)

			if res.Code != tc.HTTPStatus {
				t.Fatalf("Expected HTTP status code %d, got %d: %s", tc.HTTPStatus, res.Code, res.Body.String())
			}

			test.CompareWithResult(t, res, tc.ExpectedResponse)
		})
	}
}

func genUser(name, email string, isAdmin bool) *kubermaticv1.User {
	user := test.GenUser("", name, email)
	user.Spec.IsAdmin = isAdmin
	return user
}
