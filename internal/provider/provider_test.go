package provider

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
	"github.com/HIOK-Official/terraform-provider-hiok/internal/mockapi"
)

// These tests drive the real Terraform CLI (plan, apply, refresh, import,
// destroy) against the in-memory mock API. Terraform is taken from PATH or
// TF_ACC_TERRAFORM_PATH, or downloaded automatically.

func TestMain(m *testing.M) {
	pollInterval = 10 * time.Millisecond
	client.DefaultRetryWait = 5 * time.Millisecond
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
  regions = ["canada"]
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
  image            = "ubuntu-24.04-amd64"
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
  tier       = "hot"
  redundancy = "LRS"
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
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "region", "canada"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "status", "running"),
					resource.TestMatchResourceAttr("hiok_virtual_machine.web", "hostname", regexp.MustCompile(`^web-01-canada-[0-9a-f]+\.hiokcloud\.com$`)),
					resource.TestCheckResourceAttrSet("hiok_virtual_machine.web", "vm_id"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.web", "imported", "false"),
					resource.TestCheckResourceAttr("hiok_virtual_network.app", "status", "Available"),
					resource.TestCheckResourceAttrSet("hiok_virtual_network.app", "vnet_id"),
					resource.TestCheckResourceAttr("hiok_container.api", "status", "running"),
					resource.TestCheckResourceAttr("hiok_container.api", "dns_hostname", "api-01.hiokcloud.com"),
					resource.TestCheckResourceAttr("hiok_storage_account.assets", "status", "active"),
					resource.TestCheckResourceAttrSet("hiok_storage_account.assets", "account_id"),
					resource.TestCheckResourceAttr("data.hiok_regions.all", "ids.#", "3"),
					resource.TestCheckResourceAttr("data.hiok_regions.all", "available_ids.#", "1"),
					resource.TestCheckResourceAttr("data.hiok_regions.all", "available_ids.0", "canada"),
					resource.TestCheckResourceAttr("data.hiok_vm_images.all", "ids.0", "ubuntu-24.04-amd64"),
					resource.TestCheckResourceAttr("data.hiok_virtual_machine.web", "vcpu_count", "2"),
					resource.TestCheckResourceAttr("data.hiok_virtual_network.app", "address_space", "10.20.0.0/16"),
					resource.TestCheckResourceAttr("data.hiok_container.api", "image", "nginx:alpine"),
					resource.TestCheckResourceAttr("data.hiok_storage_account.assets", "primary_region", "canada"),
					resource.TestCheckResourceAttr("data.hiok_storage_account.assets", "tier", "hot"),
					// create-vnet ignores subnet fields; the provider rewrites the
					// platform's "default" subnet into the requested one.
					func(*terraform.State) error {
						sn := m.Subnets("app-net")
						if len(sn) != 1 || sn[0]["name"] != "web" || sn[0]["ipRange"] != "10.20.1.2-10.20.1.254" || sn[0]["size"] != "24" {
							return fmt.Errorf("subnet not configured as requested: %v", sn)
						}
						return nil
					},
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
				ImportStateVerifyIgnore: []string{"image", "vcpu_count", "ram_gb", "network_name", "username", "generate_ssh_key", "hostname", "private_key_openssh", "public_key_openssh", "imported"},
			},
			{
				ResourceName:            "hiok_virtual_network.app",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"subnet_name", "subnet_cidr", "region", "imported"},
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
				ImportStateVerifyIgnore: []string{"tier", "redundancy", "region", "imported"},
			},
		},
	})
}

// Importing resources created in the console must never plan a replacement:
// the first apply only records the configured values, with no API calls.
func TestImport_DoesNotReplace(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	m.Put("vm", "web-01", map[string]any{"regionId": "canada", "state": "running", "vCpu": 2})
	m.Put("vnet", "app-net", map[string]any{"status": "Available", "regionId": "canada", "addressSpace": "10.20.0.0/16"})
	m.Put("ct", "api-01", map[string]any{"status": "running", "image": "nginx:alpine"})
	m.Put("sa", "assets", map[string]any{"status": "active", "primaryRegion": "canada", "storageTier": "hot", "redundancy": "LRS"})

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
		plancheck.ExpectResourceAction("hiok_virtual_network.app", plancheck.ResourceActionUpdate),
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
			// The flag is cleared even where nothing needed recording, so adding a
			// subnet to the imported network later is a real change (in place).
			{
				Config: regexp.MustCompile(`address_space = "10.20.0.0/16"\n`).ReplaceAllString(config,
					"address_space = \"10.20.0.0/16\"\n  subnet_name   = \"web\"\n  subnet_cidr   = \"10.20.1.0/24\"\n"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("hiok_virtual_network.app", plancheck.ResourceActionUpdate),
				}},
				Check: func(*terraform.State) error {
					if sn := m.Subnets("app-net"); len(sn) != 1 || sn[0]["name"] != "web" {
						return fmt.Errorf("subnet not applied: %v", sn)
					}
					return nil
				},
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
			ExpectError: regexp.MustCompile(`Failed Deploying Virtual Machine.: Quota exceeded: 0 vCPU remaining`),
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
		`resource "hiok_storage_account" "x" {
  name = "s1"
  tier = "standard"
}`: `expected tier to be one of ["hot" "cool" "cold" "archive"]`,
		`resource "hiok_storage_account" "x" {
  name       = "s1"
  redundancy = "XYZ"
}`: `expected redundancy to be one of`,
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
	m.Put("vm", "web-01", map[string]any{"regionId": "canada", "state": "running"})
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

// listallcontainers ignores paging and returns everything: one call must be
// enough, and destroy must only remove the managed container.
func TestContainer_ManyContainers(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	for i := 0; i < 250; i++ {
		m.Put("ct", fmt.Sprintf("a-other-%03d", i), map[string]any{"status": "running"})
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
			if n := len(m.Names("ct")); n != 250 {
				return fmt.Errorf("expected only zz-mine to be deleted, %d containers left", n)
			}
			return nil
		},
	})
}

// Behaviour recorded from the live API: destroy-vm needs a JSON body (a query
// string gets 415), storage accounts are deleted by UUID (the name gets 400),
// and a network deleted outside Terraform answers 400 on delete.
func TestDelete_UsesRealAPIContract(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	cfg := `
provider "hiok" {}
resource "hiok_virtual_network" "n" {
  name          = "n1"
  address_space = "10.9.0.0/16"
}
resource "hiok_virtual_machine" "v" {
  name         = "vm1"
  network_name = hiok_virtual_network.n.name
}
resource "hiok_storage_account" "s" {
  name = "logs"
}`
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy: func(s *terraform.State) error {
			if err := checkAllGone(m)(s); err != nil {
				return err
			}
			var sawVMBody, sawStorageByID bool
			for _, l := range m.Log() {
				sawVMBody = sawVMBody || regexp.MustCompile(`^DELETE /api/VirtualMachine/destroy-vm \{"regions":\["canada"\],"vmName":"vm1"\}$`).MatchString(l)
				sawStorageByID = sawStorageByID || regexp.MustCompile(`^DELETE /api/StorageAccount/0{8}-0{4}-4000-8000-\d{12}$`).MatchString(l)
			}
			if !sawVMBody || !sawStorageByID {
				return fmt.Errorf("unexpected delete calls (vm body %v, storage by id %v):\n%s", sawVMBody, sawStorageByID, strings.Join(m.Log(), "\n"))
			}
			return nil
		},
		Steps: []resource.TestStep{{Config: cfg}},
	})
}

// delete-vnet answers 400 for a network that is already gone (e.g. removed in
// the console between refresh and destroy); that must not fail the destroy.
func TestVnetDelete_AlreadyGone(t *testing.T) {
	startMock(t, mockapi.Options{})
	c, err := client.New(os.Getenv("HIOK_ENDPOINT"), "", "tester@example.com", "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	d := schema.TestResourceDataRaw(t, resourceVirtualNetwork().Schema, map[string]any{"name": "gone", "address_space": "10.0.0.0/16"})
	d.SetId("gone")
	if diags := vnetDelete(context.Background(), d, c); diags.HasError() {
		t.Fatalf("destroy of an already-deleted network failed: %v", diags)
	}
	if d.Id() != "" {
		t.Fatal("resource not removed from state")
	}
}

// Unknown regions fail in seconds with the valid list; the live API would
// hang for ~100s and return a Cloudflare 524.
func TestRegion_UnknownFailsFast(t *testing.T) {
	startMock(t, mockapi.Options{})
	for region, want := range map[string]string{
		"south-india":   `unknown region "south-india"; available regions: canada`,
		"central-india": `region "central-india" exists but is not available`,
	} {
		t.Run(region, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV5ProviderFactories: factories,
				Steps: []resource.TestStep{{
					Config:      fmt.Sprintf("provider \"hiok\" {}\nresource \"hiok_virtual_machine\" \"v\" {\n  name   = \"vm1\"\n  region = %q\n}\n", region),
					ExpectError: regexp.MustCompile(regexp.QuoteMeta(want)),
				}},
			})
		})
	}
}

// Without `regions`, the provider uses the first available region from the API.
func TestRegion_DefaultsToFirstAvailable(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {}
resource "hiok_storage_account" "s" {
  name = "logs"
}`,
			Check: resource.TestCheckResourceAttr("hiok_storage_account.s", "region", "canada"),
		}},
	})
}

// The reason for a failed VM create is nested in data.data.
func TestCreate_BadImageShowsReason(t *testing.T) {
	startMock(t, mockapi.Options{})
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {}
resource "hiok_virtual_machine" "v" {
  name  = "vm1"
  image = "ubuntu-24.04"
}`,
			ExpectError: regexp.MustCompile(`Failed Deploying Virtual Machine.: Failed to download VM image`),
		}},
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

func TestUsableRange(t *testing.T) {
	for cidr, want := range map[string][2]string{
		"10.20.1.0/24":   {"10.20.1.2-10.20.1.254", "24"},
		"10.231.0.0/16":  {"10.231.0.2-10.231.255.254", "16"},
		"192.168.0.0/30": {"192.168.0.2-192.168.0.2", "30"},
	} {
		r, size, err := usableRange(cidr)
		if err != nil || r != want[0] || size != want[1] {
			t.Errorf("usableRange(%s) = %q, %q, %v; want %q, %q", cidr, r, size, err, want[0], want[1])
		}
	}
	if _, _, err := usableRange("2001:db8::/64"); err == nil {
		t.Error("IPv6 subnet_cidr should be rejected")
	}
}

// generate_ssh_key creates the pair locally (the platform's own option keeps
// no retrievable key), sends the public half, and exposes the private half.
// Connection details come from /connect; power_state changes in place.
func TestVM_KeysConnectionAndPowerState(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	cfg := func(power string) string {
		return fmt.Sprintf(`
provider "hiok" {}
resource "hiok_virtual_machine" "v" {
  name             = "vm1"
  username         = "ubuntu"
  generate_ssh_key = true
  power_state      = %q
}`, power)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{
			{
				Config: cfg("running"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("hiok_virtual_machine.v", "private_key_openssh", regexp.MustCompile(`^-----BEGIN OPENSSH PRIVATE KEY-----\n`)),
					resource.TestMatchResourceAttr("hiok_virtual_machine.v", "public_key_openssh", regexp.MustCompile(`^ssh-ed25519 \S+ vm1$`)),
					resource.TestMatchResourceAttr("hiok_virtual_machine.v", "private_ip", regexp.MustCompile(`^10\.20\.0\.\d+$`)),
					resource.TestMatchResourceAttr("hiok_virtual_machine.v", "ssh_command", regexp.MustCompile(`^ssh ubuntu@10\.20\.0\.\d+$`)),
					resource.TestCheckResourceAttr("hiok_virtual_machine.v", "power_state", "running"),
					func(s *terraform.State) error {
						pub := s.RootModule().Resources["hiok_virtual_machine.v"].Primary.Attributes["public_key_openssh"]
						for _, l := range m.Log() {
							if strings.Contains(l, "create-vm") {
								if !strings.Contains(l, `"sshPublicKey":"`+pub+`"`) || strings.Contains(l, "generateSshKey") {
									return fmt.Errorf("create-vm did not send the generated public key: %s", l)
								}
								return nil
							}
						}
						return fmt.Errorf("no create-vm call")
					},
				),
			},
			{
				Config: cfg("stopped"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("hiok_virtual_machine.v", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hiok_virtual_machine.v", "power_state", "stopped"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.v", "status", "shutoff"),
				),
			},
			{
				Config: cfg("running"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("hiok_virtual_machine.v", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("hiok_virtual_machine.v", "status", "running"),
			},
		},
	})
}

// Tier, redundancy and display name change in place (PUT), keeping the data.
func TestStorage_UpdateInPlace(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	cfg := func(tier, red, display string) string {
		return fmt.Sprintf(`
provider "hiok" {}
resource "hiok_storage_account" "s" {
  name         = "logs"
  tier         = %q
  redundancy   = %q
  display_name = %q
}`, tier, red, display)
	}
	var id string
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{
			{
				Config: cfg("hot", "LRS", "Logs"),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources["hiok_storage_account.s"].Primary.Attributes["account_id"]
					return nil
				},
			},
			{
				Config: cfg("cool", "ZRS", "Logs (cool)"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("hiok_storage_account.s", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hiok_storage_account.s", "tier", "cool"),
					resource.TestCheckResourceAttr("hiok_storage_account.s", "redundancy", "ZRS"),
					func(s *terraform.State) error {
						if got := s.RootModule().Resources["hiok_storage_account.s"].Primary.Attributes["account_id"]; got != id {
							return fmt.Errorf("account was replaced: %s -> %s", id, got)
						}
						return nil
					},
				),
			},
		},
	})
}

// Changing the subnet updates it in place instead of replacing the network.
func TestVnet_SubnetUpdateInPlace(t *testing.T) {
	m := startMock(t, mockapi.Options{})
	cfg := func(name, cidr string) string {
		return fmt.Sprintf(`
provider "hiok" {}
resource "hiok_virtual_network" "n" {
  name          = "n1"
  address_space = "10.9.0.0/16"
  subnet_name   = %q
  subnet_cidr   = %q
}`, name, cidr)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{
			{Config: cfg("web", "10.9.1.0/24")},
			{
				Config: cfg("app", "10.9.8.0/22"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("hiok_virtual_network.n", plancheck.ResourceActionUpdate),
				}},
				Check: func(*terraform.State) error {
					sn := m.Subnets("n1")
					if len(sn) != 1 || sn[0]["name"] != "app" || sn[0]["ipRange"] != "10.9.8.2-10.9.11.254" {
						return fmt.Errorf("subnet not updated in place: %v", sn)
					}
					return nil
				},
			},
		},
	})
}

// On the live platform a stop-vm was followed by minutes of 502s (sign-in
// included). Waits and sign-in must ride that out instead of failing.
func TestVM_SurvivesOutageAfterStop(t *testing.T) {
	m := startMock(t, mockapi.Options{OutageAfterStop: 12})
	cfg := func(power string) string {
		return fmt.Sprintf(`
provider "hiok" {}
resource "hiok_virtual_machine" "v" {
  name        = "vm1"
  power_state = %q
}`, power)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{
			{Config: cfg("running")},
			{Config: cfg("stopped"), Check: resource.TestCheckResourceAttr("hiok_virtual_machine.v", "power_state", "stopped")},
		},
	})
}

// A pipeline signs in with a service principal (client_id / client_secret), with no
// email or password anywhere, and renews its token when the API says it expired.
func TestServicePrincipal_SignsInAndRenews(t *testing.T) {
	m := startMock(t, mockapi.Options{ExpireTokenAfter: 3})
	t.Setenv("HIOK_EMAIL", "")
	t.Setenv("HIOK_PASSWORD", "")
	t.Setenv("HIOK_CLIENT_ID", "11111111-2222-3333-4444-555555555555")
	t.Setenv("HIOK_CLIENT_SECRET", "sp-secret")
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             checkAllGone(m),
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {}
resource "hiok_storage_account" "s" {
  name = "splogs"
}`,
			Check: func(*terraform.State) error {
				var sp, password int
				for _, line := range m.Log() {
					switch {
					case strings.HasPrefix(line, "POST /api/OAuth/token/client"):
						sp++
					case strings.HasPrefix(line, "POST /api/OAuth/token "):
						password++
					}
				}
				if sp < 2 || password != 0 {
					return fmt.Errorf("want service-principal sign-ins (incl. a renewal) and no password sign-in; got %d and %d", sp, password)
				}
				return nil
			},
		}},
	})
}

func TestServicePrincipal_WrongSecretIsRefused(t *testing.T) {
	startMock(t, mockapi.Options{})
	t.Setenv("HIOK_EMAIL", "")
	t.Setenv("HIOK_PASSWORD", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
provider "hiok" {
  client_id     = "11111111-2222-3333-4444-555555555555"
  client_secret = "wrong"
}
resource "hiok_storage_account" "s" { name = "nope" }`,
			ExpectError: regexp.MustCompile(`Invalid client credentials`),
		}},
	})
}
