package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// TestAccLive runs create → re-plan → import → destroy against a real HIOK
// deployment and checks through the API that nothing is left behind. It only
// runs with TF_ACC=1 and HIOK_ENDPOINT plus credentials, and it creates real
// (billable) resources named tf-acc-<random>:
//
//	TF_ACC=1 HIOK_ENDPOINT=https://test.hiokcloud.com HIOK_EMAIL=... HIOK_PASSWORD=... \
//	  go test ./internal/provider -run TestAccLive -v -timeout 60m
//
// HIOK_ACC_REGION and HIOK_ACC_IMAGE override the region and VM image.
func TestAccLive(t *testing.T) {
	if os.Getenv("TF_ACC") == "" || os.Getenv("HIOK_ENDPOINT") == "" {
		t.Skip("set TF_ACC=1 and HIOK_ENDPOINT (+ HIOK_TOKEN or HIOK_EMAIL/HIOK_PASSWORD) to run against a live deployment")
	}
	pollInterval = pollIntervalLive

	region := envOr("HIOK_ACC_REGION", "canada")
	image := envOr("HIOK_ACC_IMAGE", defaultVMImage)
	suffix := acctest.RandStringFromCharSet(6, "abcdefghijklmnopqrstuvwxyz0123456789")
	vnet, vm, ct, sa := "tf-acc-net-"+suffix, "tf-acc-vm-"+suffix, "tf-acc-ct-"+suffix, "tfacc"+suffix

	configFor := func(power, tier, subnetCIDR string) string {
		return fmt.Sprintf(`
provider "hiok" {
  regions = [%[1]q]
}
data "hiok_regions" "all" {}
data "hiok_vm_images" "all" {}

resource "hiok_virtual_network" "t" {
  name          = %[3]q
  address_space = "10.231.0.0/16"
  subnet_name   = "default"
  subnet_cidr   = %[9]q
}
resource "hiok_virtual_machine" "t" {
  name             = %[4]q
  image            = %[2]q
  vcpu_count       = 1
  ram_gb           = 1
  disk_size_gb     = 10
  network_name     = hiok_virtual_network.t.name
  username         = "ubuntu"
  generate_ssh_key = true
  power_state      = %[7]q
}
resource "hiok_container" "t" {
  name  = %[5]q
  image = "nginx:alpine"
  env   = ["TF_ACC=1"]
}
resource "hiok_storage_account" "t" {
  name = %[6]q
  tier = %[8]q
}
`, region, image, vnet, vm, ct, sa, power, tier, subnetCIDR)
	}
	config := configFor("running", "hot", "10.231.1.0/24")
	changed := configFor("stopped", "cool", "10.231.4.0/22")

	var c *client.Client
	gone := func(*terraform.State) error {
		if c == nil {
			var err error
			if c, err = client.New(os.Getenv("HIOK_ENDPOINT"), os.Getenv("HIOK_TOKEN"), os.Getenv("HIOK_EMAIL"), os.Getenv("HIOK_PASSWORD"), []string{region}); err != nil {
				return err
			}
		}
		ctx := context.Background()
		for name, exists := range map[string]finder{vm: vmExists, vnet: vnetExists, ct: containerExists, sa: storageExists} {
			found, err := exists(ctx, c, name)
			if err != nil {
				return err
			}
			if found {
				return fmt.Errorf("%s still exists after destroy", name)
			}
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV5ProviderFactories: factories,
		CheckDestroy:             gone,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hiok_virtual_machine.t", "id", vm),
					resource.TestCheckResourceAttr("hiok_virtual_machine.t", "status", "running"),
					resource.TestCheckResourceAttr("hiok_virtual_machine.t", "region", region),
					resource.TestCheckResourceAttrSet("hiok_virtual_machine.t", "vm_id"),
					resource.TestCheckResourceAttrSet("hiok_virtual_machine.t", "hostname"),
					resource.TestMatchResourceAttr("hiok_virtual_machine.t", "private_ip", regexp.MustCompile(`^10\.231\.`)),
					resource.TestCheckResourceAttrSet("hiok_virtual_machine.t", "ssh_command"),
					resource.TestMatchResourceAttr("hiok_virtual_machine.t", "private_key_openssh", regexp.MustCompile(`BEGIN OPENSSH PRIVATE KEY`)),
					resource.TestCheckResourceAttrSet("hiok_virtual_network.t", "vnet_id"),
					resource.TestCheckResourceAttrSet("hiok_container.t", "dns_hostname"),
					resource.TestCheckResourceAttrSet("hiok_storage_account.t", "account_id"),
					resource.TestCheckResourceAttr("data.hiok_regions.all", "available_ids.0", region),
					resource.TestCheckResourceAttr("hiok_virtual_network.t", "id", vnet),
					resource.TestCheckResourceAttr("hiok_container.t", "id", ct),
					resource.TestCheckResourceAttr("hiok_storage_account.t", "id", sa),
				),
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			// Power, storage tier and subnet change in place, without replacement.
			{
				Config: changed,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("hiok_virtual_machine.t", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("hiok_storage_account.t", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("hiok_virtual_network.t", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hiok_virtual_machine.t", "power_state", "stopped"),
					resource.TestCheckResourceAttr("hiok_storage_account.t", "tier", "cool"),
				),
			},
			{
				Config: config,
				Check:  resource.TestCheckResourceAttr("hiok_virtual_machine.t", "power_state", "running"),
			},
			{ResourceName: "hiok_virtual_machine.t", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"image", "vcpu_count", "ram_gb", "disk_size_gb", "network_name", "username", "generate_ssh_key", "private_key_openssh", "public_key_openssh", "imported"}},
			{ResourceName: "hiok_virtual_network.t", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"address_space", "subnet_name", "subnet_cidr", "imported"}},
			{ResourceName: "hiok_container.t", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"image", "env", "imported"}},
			{ResourceName: "hiok_storage_account.t", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"tier", "redundancy", "display_name", "imported"}},
		},
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
