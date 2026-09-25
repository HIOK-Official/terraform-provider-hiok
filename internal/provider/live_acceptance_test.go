package provider

import (
	"context"
	"fmt"
	"os"
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

	region := envOr("HIOK_ACC_REGION", "south-india")
	image := envOr("HIOK_ACC_IMAGE", "ubuntu-24.04")
	suffix := acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	vnet, vm, ct, sa := "tf-acc-net-"+suffix, "tf-acc-vm-"+suffix, "tf-acc-ct-"+suffix, "tfacc"+suffix

	config := fmt.Sprintf(`
provider "hiok" {
  regions = [%[1]q]
}
data "hiok_regions" "all" {}
data "hiok_vm_images" "all" {}

resource "hiok_virtual_network" "t" {
  name          = %[3]q
  address_space = "10.231.0.0/16"
  subnet_name   = "default"
  subnet_cidr   = "10.231.1.0/24"
}
resource "hiok_virtual_machine" "t" {
  name             = %[4]q
  image            = %[2]q
  vcpu_count       = 1
  ram_gb           = 1
  network_name     = hiok_virtual_network.t.name
  username         = "ubuntu"
  generate_ssh_key = true
}
resource "hiok_container" "t" {
  name  = %[5]q
  image = "nginx:alpine"
  env   = ["TF_ACC=1"]
}
resource "hiok_storage_account" "t" {
  name = %[6]q
}
`, region, image, vnet, vm, ct, sa)

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
					resource.TestCheckResourceAttrSet("hiok_virtual_machine.t", "status"),
					resource.TestCheckResourceAttr("hiok_virtual_network.t", "id", vnet),
					resource.TestCheckResourceAttr("hiok_container.t", "id", ct),
					resource.TestCheckResourceAttr("hiok_storage_account.t", "id", sa),
				),
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ResourceName: "hiok_virtual_machine.t", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"image", "vcpu_count", "ram_gb", "network_name", "username", "generate_ssh_key", "imported"}},
			{ResourceName: "hiok_virtual_network.t", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"address_space", "subnet_name", "subnet_cidr", "imported"}},
			{ResourceName: "hiok_container.t", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"image", "env", "imported"}},
			{ResourceName: "hiok_storage_account.t", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"tier", "redundancy", "imported"}},
		},
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
