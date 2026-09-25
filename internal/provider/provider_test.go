package provider

import (
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/mockapi"
)

// These tests drive the real Terraform CLI (plan, apply, refresh, import,
// destroy) against the in-memory mock API. Terraform is taken from PATH or
// TF_ACC_TERRAFORM_PATH, or downloaded automatically.

func TestMain(m *testing.M) {
	pollInterval = 10 * time.Millisecond
	os.Exit(m.Run())
}

var factories = map[string]func() (tfprotov5.ProviderServer, error){
	"hiok": func() (tfprotov5.ProviderServer, error) { return schema.NewGRPCProviderServer(New()), nil },
}

func startMock(t *testing.T, opt mockapi.Options) *mockapi.Server {
	t.Helper()
	m := mockapi.New(opt)
	ts := httptest.NewServer(m.Handler())
	t.Cleanup(ts.Close)
	t.Setenv("HIOK_ENDPOINT", ts.URL)
	t.Setenv("HIOK_TOKEN", "")
	t.Setenv("HIOK_EMAIL", "tester@example.com")
	t.Setenv("HIOK_PASSWORD", "secret")
	return m
}

// checkAllGone is a CheckDestroy that asserts the API holds nothing any more.
func checkAllGone(m *mockapi.Server) resource.TestCheckFunc {
	return func(*terraform.State) error {
		for _, kind := range []string{"vm", "vnet", "ct", "sa"} {
			if left := m.Names(kind); len(left) > 0 {
				return fmt.Errorf("destroy left %s resources behind: %v", kind, left)
			}
		}
		return nil
	}
}

const fullConfig = `
provider "hiok" {
  regions = ["south-india"]
}

data "hiok_regions" "all" {}
data "hiok_vm_images" "all" {}

resource "hiok_virtual_network" "app" {
  name          = "app-net"
  address_space = "10.20.0.0/16"
  subnet_name   = "web"
  subnet_cidr   = "10.20.1.0/24"
}

# No region on purpose: it must default without causing drift.
resource "hiok_virtual_machine" "web" {
  name             = "web-01"
  image            = "ubuntu-24.04"
  vcpu_count       = 2
  ram_gb           = 4
  network_name     = hiok_virtual_network.app.name
  username         = "ubuntu"
  generate_ssh_key = true
}

resource "hiok_container" "api" {
  name  = "api-01"
  image = "nginx:alpine"
  env   = ["NODE_ENV=production"]
}

resource "hiok_storage_account" "assets" {
  name       = "assets"
  tier       = "standard"
  redundancy = "lrs"
}

data "hiok_virtual_machine" "web" {
  name = hiok_virtual_machine.web.name
}
data "hiok_virtual_network" "app" {
  name = hiok_virtual_network.app.name
}
data "hiok_container" "api" {
  name = hiok_container.api.name
}
data "hiok_storage_account" "assets" {
  name = hiok_storage_account.assets.name
}
`

// Full lifecycle with async provisioning: create waits for resources to show
// up, the follow-up plan is empty (no region drift), every resource imports
// cleanly, and destroy waits until everything is really gone.
func TestLifecycle_AllResources(t *testing.T) {
	m := startMock(t, mockapi.Options{HiddenReads: 3})

	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{
			{
				Config: fullConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "id", "web-01"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "region", "south-india"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "status", "running"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "private_ip", "10.20.1.5"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "imported", "false"),
					resource.TestCheckResourceAttr("hiok_virtual_network.app", "status", "active"),
					resource.TestCheckResourceAttr("hiok_container.api", "status", "running"),
					resource.TestCheckResourceAttr("hiok_storage_account.assets", "status", "available"),
					resource.TestCheckResourceAttr("data.hiok_regions.all", "ids.#", "2"),
					resource.TestCheckResourceAttr("data.hiok_vm_images.all", "names.0", "ubuntu-24.04"),
					resource.TestCheckResourceAttr("data.hiok_virtual_machine.web", "private_ip", "10.20.1.5"),
					resource.TestCheckResourceAttr("data.hiok_virtual_network.app", "address_space", "10.20.0.0/16"),
					resource.TestCheckResourceAttr("data.hiok_container.api", "image", "nginx:alpine"),
					resource.TestCheckResourceAttr("data.hiok_storage_account.assets", "primary_region", "south-india"),
					func(*terraform.State) error {
						for kind, want := range map[string]string{"vm": "web-01", "vnet": "app-net", "ct": "api-01", "sa": "assets"} {
							if !slices.Equal(m.Names(kind), []string{want}) {
								return fmt.Errorf("API %s = %v, want [%s]", kind, m.Names(kind), want)
							}
						}
						return nil
					},
				),
			},
			// Re-applying the same config must be a no-op (the framework also
			// fails any step whose post-apply plan is not empty).
			{
				Config: fullConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:            "hiok_virtual_machine.web",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"image", "vcpu_count", "ram_gb", "network_name", "username", "generate_ssh_key", "imported"},
			},
			{
				ResourceName:            "hiok_virtual_network.app",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"subnet_name", "subnet_cidr", "imported"},
			},
			{
				ResourceName:            "hiok_container.api",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"image", "env", "imported"},
			},
			{
				ResourceName:            "hiok_storage_account.assets",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"tier", "redundancy", "imported"},
			},
		},
	})
}

// Importing resources created in the console must never plan a replacement:
// the first apply only records the configured values, with no API calls.
func TestImport_DoesNotReplace(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	m.Put("vm", "web-01", map[string]any{"regionId": "south-india", "status": "running", "privateIp": "10.0.0.9"})
	m.Put("vnet", "app-net", map[string]any{"status": "active", "addressSpace": "10.20.0.0/16"})
	m.Put("ct", "api-01", map[string]any{"status": "running", "image": "nginx:alpine"})
	m.Put("sa", "assets", map[string]any{"status": "available"})

	config := `
provider "hiok" {}

import {
  to = hiok_virtual_machine.web
  id = "web-01"
}
import {
  to = hiok_virtual_network.app
  id = "app-net"
}
import {
  to = hiok_container.api
  id = "api-01"
}
import {
  to = hiok_storage_account.assets
  id = "assets"
}

resource "hiok_virtual_network" "app" {
  name          = "app-net"
  address_space = "10.20.0.0/16"
}
resource "hiok_virtual_machine" "web" {
  name         = "web-01"
  vcpu_count   = 2
  ram_gb       = 4
  network_name = hiok_virtual_network.app.name
  username     = "ubuntu"
}
resource "hiok_container" "api" {
  name  = "api-01"
  image = "nginx:alpine"
}
resource "hiok_storage_account" "assets" {
  name = "assets"
}
`
	noReplace := []plancheck.PlanCheck{
		plancheck.ExpectResourceAction("hiok_virtual_machine.web", plancheck.ResourceActionUpdate),
		plancheck.ExpectResourceAction("hiok_virtual_network.app", plancheck.ResourceActionNoop),
		plancheck.ExpectResourceAction("hiok_container.api", plancheck.ResourceActionUpdate),
		plancheck.ExpectResourceAction("hiok_storage_account.assets", plancheck.ResourceActionUpdate),
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: noReplace},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "vcpu_count", "2"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "imported", "false"),
					resource.TestCheckResourceAttr("hiok_virtual_network.app", "address_space", "10.20.0.0/16"),
				),
			},
			// After the values are recorded, changing one forces replacement again.
			{
				Config: regexp.MustCompile(`vcpu_count   = 2`).ReplaceAllString(config, `vcpu_count   = 4`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("hiok_virtual_machine.web", plancheck.ResourceActionReplace),
				}},
			},
		},
	})
}

// The API reports failures as 200 {"success": false}; the real message must
// reach the user and nothing may be left in state.
func TestCreate_APIErrorIsSurfaced(t *testing.T) {
	startMock(t, mockapi.Options{FailCreate: "Quota exceeded: 0 vCPU remaining"})
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {}
resource "hiok_virtual_machine" "web" {
  name = "web-01"
}`,
			ExpectError: regexp.MustCompile(`Quota exceeded: 0 vCPU remaining`),
		}},
	})
}

// Names that would break URLs or the API's "<owner>#<name>" scoping are
// rejected at plan time instead of deleting the wrong VM at destroy time.
func TestValidation(t *testing.T) {
	startMock(t, mockapi.Options{})
	cases := map[string]string{
		`resource "hiok_virtual_machine" "x" { name = "dev&test" }`:   `must start with a letter or digit`,
		`resource "hiok_virtual_machine" "x" { name = "db+replica" }`: `must start with a letter or digit`,
		`resource "hiok_virtual_machine" "x" { name = "owner#vm" }`:   `must start with a letter or digit`,
		`resource "hiok_virtual_machine" "x" {
  name       = "vm-1"
  vcpu_count = 0
}`: `expected vcpu_count to be in the range`,
		`resource "hiok_virtual_network" "x" {
  name          = "n"
  address_space = "10.0.0.0/33"
}`: `to be a valid CIDR Value`,
		`resource "hiok_container" "x" {
  name  = "c"
  image = "nginx"
  env   = ["NOEQUALS"]
}`: `must look like "KEY=value"`,
		`resource "hiok_virtual_machine" "x" {
  name             = "vm-1"
  ssh_public_key   = "ssh-ed25519 AAAA"
  generate_ssh_key = true
}`: `conflicts with`,
	}
	for cfg, want := range cases {
		t.Run(want, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV5ProviderFactories: factories,
				Steps: []resource.TestStep{{
					Config:      "provider \"hiok\" {}\n" + cfg,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(regexp.QuoteMeta(want)),
				}},
			})
		})
	}
}

func TestVnet_SubnetOutsideAddressSpace(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {}
resource "hiok_virtual_network" "x" {
  name          = "n1"
  address_space = "10.20.0.0/16"
  subnet_name   = "web"
  subnet_cidr   = "10.99.1.0/24"
}`,
			ExpectError: regexp.MustCompile(`not inside address_space`),
		}},
	})
}

// A resource deleted in the console is recreated on the next apply.
func TestDriftRecreatesDeletedResource(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	cfg := `
provider "hiok" {}
resource "hiok_storage_account" "s" {
  name = "logs"
}`
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() { m.Remove("sa", "logs") },
				Config:    cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("hiok_storage_account.s", plancheck.ResourceActionCreate),
				}},
				Check: func(*terraform.State) error {
					if !slices.Equal(m.Names("sa"), []string{"logs"}) {
						return fmt.Errorf("storage account was not recreated: %v", m.Names("sa"))
					}
					return nil
				},
			},
		},
	})
}

// Creating over an existing name fails with an import hint instead of
// silently adopting (and later destroying) someone else's resource.
func TestCreate_ExistingNameSuggestsImport(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	m.Put("vm", "web-01", map[string]any{"regionId": "south-india"})
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {}
resource "hiok_virtual_machine" "web" {
  name = "web-01"
}`,
			ExpectError: regexp.MustCompile(`already exists; import it`),
		}},
	})
}

// More containers than one page: the managed one must still be found.
func TestContainer_Pagination(t *testing.T) {
	old := containerPageSize
	containerPageSize = 5
	t.Cleanup(func() { containerPageSize = old })

	m := startMock(t, mockapi.Options{})
	for i := 0; i < 12; i++ {
		m.Put("ct", fmt.Sprintf("a-other-%02d", i), map[string]any{"status": "running"})
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {}
resource "hiok_container" "z" {
  name  = "zz-mine"
  image = "nginx:alpine"
}`,
			Check: resource.TestCheckResourceAttr("hiok_container.z", "status", "running"),
		}},
		CheckDestroy: func(*terraform.State) error {
			if n := len(m.Names("ct")); n != 12 {
				return fmt.Errorf("expected only zz-mine to be deleted, %d containers left", n)
			}
			return nil
		},
	})
}

// Expired tokens are refreshed with email/password; transient 503s retried.
func TestClient_TokenRefreshAndRetry(t *testing.T) {
	m := startMock(t, mockapi.Options{ExpireTokenAfter: 3, Transient503: 1})
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {}
resource "hiok_virtual_network" "n" {
  name          = "n1"
  address_space = "10.1.0.0/16"
}`,
		}},
	})
}

func TestProviderConfig_Errors(t *testing.T) {
	startMock(t, mockapi.Options{})
	t.Setenv("HIOK_ENDPOINT", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config:      `data "hiok_regions" "r" {}`,
			ExpectError: regexp.MustCompile(`endpoint must be configured`),
		}},
	})
}

func TestProviderInternalValidate(t *testing.T) {
	if err := New().InternalValidate(); err != nil {
		t.Fatal(err)
	}
}
