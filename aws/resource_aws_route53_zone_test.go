package aws

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/route53"
	multierror "github.com/hashicorp/go-multierror"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestCleanZoneID(t *testing.T) {
	cases := []struct {
		Input, Output string
	}{
		{"/hostedzone/foo", "foo"},
		{"/change/foo", "/change/foo"},
		{"/bar", "/bar"},
	}

	for _, tc := range cases {
		actual := cleanZoneID(tc.Input)
		if actual != tc.Output {
			t.Fatalf("input: %s\noutput: %s", tc.Input, actual)
		}
	}
}

func TestCleanChangeID(t *testing.T) {
	cases := []struct {
		Input, Output string
	}{
		{"/hostedzone/foo", "/hostedzone/foo"},
		{"/change/foo", "foo"},
		{"/bar", "/bar"},
	}

	for _, tc := range cases {
		actual := cleanChangeID(tc.Input)
		if actual != tc.Output {
			t.Fatalf("input: %s\noutput: %s", tc.Input, actual)
		}
	}
}

func TestTrimTrailingPeriod(t *testing.T) {
	cases := []struct {
		Input  interface{}
		Output string
	}{
		{"example.com", "example.com"},
		{"example.com.", "example.com"},
		{"www.example.com.", "www.example.com"},
		{"", ""},
		{".", "."},
		{aws.String("example.com"), "example.com"},
		{aws.String("example.com."), "example.com"},
		{(*string)(nil), ""},
		{42, ""},
		{nil, ""},
	}

	for _, tc := range cases {
		actual := trimTrailingPeriod(tc.Input)
		if actual != tc.Output {
			t.Fatalf("input: %s\noutput: %s", tc.Input, actual)
		}
	}
}

// add sweeper to delete resources
func init() {
	resource.AddTestSweepers("aws_route53_zone", &resource.Sweeper{
		Name: "aws_route53_zone",
		Dependencies: []string{
			"aws_service_discovery_http_namespace",
			"aws_service_discovery_public_dns_namespace",
			"aws_service_discovery_private_dns_namespace",
			"aws_elb",
			"aws_route53_key_signing_key",
		},
		F: testSweepRoute53Zones,
	})
}

func hostedZonesToPreserve() []string {
	return []string{
		"acmetest.hashicorp.engineering",
		"tfacc.hashicorptest.com",
		"aws.tfacc.hashicorptest.com",
		"hashicorp.com",
		"terraform-provider-aws-acctest-acm.com",
	}
}

func testSweepRoute53Zones(region string) error {
	client, err := sharedClientForRegion(region)

	if err != nil {
		return fmt.Errorf("error getting client: %s", err)
	}

	conn := client.(*AWSClient).r53conn
	sweepResources := make([]*testSweepResource, 0)
	var errs *multierror.Error

	input := &route53.ListHostedZonesInput{}

	err = conn.ListHostedZonesPages(input, func(page *route53.ListHostedZonesOutput, lastPage bool) bool {
		if page == nil {
			return !lastPage
		}

	MAIN:
		for _, detail := range page.HostedZones {
			if detail == nil {
				continue
			}

			id := aws.StringValue(detail.Id)

			for _, domain := range hostedZonesToPreserve() {
				if strings.Contains(aws.StringValue(detail.Name), domain) {
					log.Printf("[DEBUG] Skipping Route53 Hosted Zone (%s): %s", domain, id)
					continue MAIN
				}
			}

			r := resourceAwsRoute53Zone()
			d := r.Data(nil)
			d.SetId(id)
			d.Set("force_destroy", true)
			d.Set("name", detail.Name)

			sweepResources = append(sweepResources, NewTestSweepResource(r, d, client))
		}

		return !lastPage
	})

	if err != nil {
		errs = multierror.Append(errs, fmt.Errorf("error describing Route53 Hosted Zones for %s: %w", region, err))
	}

	if err = testSweepResourceOrchestratorContext(context.Background(), sweepResources, 0*time.Minute, 1*time.Minute, 10*time.Second, 18*time.Second, 10*time.Minute); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("error sweeping Route53 Hosted Zones for %s: %w", region, err))
	}

	if testSweepSkipSweepError(errs.ErrorOrNil()) {
		log.Printf("[WARN] Skipping Route53 Hosted Zones sweep for %s: %s", region, errs)
		return nil
	}

	return errs.ErrorOrNil()
}

func TestAccAWSRoute53Zone_basic(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	resourceName := "aws_route53_zone.test"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfig(zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					testAccMatchResourceAttrGlobalARNNoAccount(resourceName, "arn", "route53", regexp.MustCompile("hostedzone/.+")),
					resource.TestCheckResourceAttr(resourceName, "name", zoneName),
					resource.TestCheckResourceAttr(resourceName, "name_servers.#", "4"),
					resource.TestCheckResourceAttr(resourceName, "tags.%", "0"),
					resource.TestCheckResourceAttr(resourceName, "vpc.#", "0"),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy"},
			},
		},
	})
}

func TestAccAWSRoute53Zone_disappears(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	resourceName := "aws_route53_zone.test"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfig(zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					testAccCheckRoute53ZoneDisappears(&zone),
				),
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccAWSRoute53Zone_multiple(t *testing.T) {
	var zone0, zone1, zone2, zone3, zone4 route53.GetHostedZoneOutput

	domainName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigMultiple(domainName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists("aws_route53_zone.test.0", &zone0),
					testAccCheckDomainName(&zone0, fmt.Sprintf("subdomain0.%s.", domainName)),
					testAccCheckRoute53ZoneExists("aws_route53_zone.test.1", &zone1),
					testAccCheckDomainName(&zone1, fmt.Sprintf("subdomain1.%s.", domainName)),
					testAccCheckRoute53ZoneExists("aws_route53_zone.test.2", &zone2),
					testAccCheckDomainName(&zone2, fmt.Sprintf("subdomain2.%s.", domainName)),
					testAccCheckRoute53ZoneExists("aws_route53_zone.test.3", &zone3),
					testAccCheckDomainName(&zone3, fmt.Sprintf("subdomain3.%s.", domainName)),
					testAccCheckRoute53ZoneExists("aws_route53_zone.test.4", &zone4),
					testAccCheckDomainName(&zone4, fmt.Sprintf("subdomain4.%s.", domainName)),
				),
			},
		},
	})
}

func TestAccAWSRoute53Zone_Comment(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	resourceName := "aws_route53_zone.test"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigComment(zoneName, "comment1"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "comment", "comment1"),
				),
			},
			{
				Config: testAccRoute53ZoneConfigComment(zoneName, "comment2"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "comment", "comment2"),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy"},
			},
		},
	})
}

func TestAccAWSRoute53Zone_DelegationSetID(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	delegationSetResourceName := "aws_route53_delegation_set.test"
	resourceName := "aws_route53_zone.test"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigDelegationSetID(zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttrPair(resourceName, "delegation_set_id", delegationSetResourceName, "id"),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy"},
			},
		},
	})
}

func TestAccAWSRoute53Zone_ForceDestroy(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	resourceName := "aws_route53_zone.test"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigForceDestroy(zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					// Add >100 records to verify pagination works ok
					testAccCreateRandomRoute53RecordsInZoneId(&zone, 100),
					testAccCreateRandomRoute53RecordsInZoneId(&zone, 5),
				),
			},
		},
	})
}

func TestAccAWSRoute53Zone_ForceDestroy_TrailingPeriod(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	resourceName := "aws_route53_zone.test"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigForceDestroyTrailingPeriod(zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					// Add >100 records to verify pagination works ok
					testAccCreateRandomRoute53RecordsInZoneId(&zone, 100),
					testAccCreateRandomRoute53RecordsInZoneId(&zone, 5),
				),
			},
		},
	})
}

func TestAccAWSRoute53Zone_Tags(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	resourceName := "aws_route53_zone.test"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigTagsSingle(zoneName, "tag1key", "tag1value"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "tags.%", "1"),
					resource.TestCheckResourceAttr(resourceName, "tags.tag1key", "tag1value"),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy"},
			},
			{
				Config: testAccRoute53ZoneConfigTagsMultiple(zoneName, "tag1key", "tag1valueupdated", "tag2key", "tag2value"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "tags.%", "2"),
					resource.TestCheckResourceAttr(resourceName, "tags.tag1key", "tag1valueupdated"),
					resource.TestCheckResourceAttr(resourceName, "tags.tag2key", "tag2value"),
				),
			},
			{
				Config: testAccRoute53ZoneConfigTagsSingle(zoneName, "tag2key", "tag2value"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "tags.%", "1"),
					resource.TestCheckResourceAttr(resourceName, "tags.tag2key", "tag2value"),
				),
			},
		},
	})
}

func TestAccAWSRoute53Zone_VPC_Single(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	rName := acctest.RandomWithPrefix("tf-acc-test")
	resourceName := "aws_route53_zone.test"
	vpcResourceName := "aws_vpc.test1"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigVPCSingle(rName, zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "vpc.#", "1"),
					testAccCheckRoute53ZoneAssociatesWithVpc(vpcResourceName, &zone),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy"},
			},
		},
	})
}

func TestAccAWSRoute53Zone_VPC_Multiple(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	rName := acctest.RandomWithPrefix("tf-acc-test")
	resourceName := "aws_route53_zone.test"
	vpcResourceName1 := "aws_vpc.test1"
	vpcResourceName2 := "aws_vpc.test2"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigVPCMultiple(rName, zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "vpc.#", "2"),
					testAccCheckRoute53ZoneAssociatesWithVpc(vpcResourceName1, &zone),
					testAccCheckRoute53ZoneAssociatesWithVpc(vpcResourceName2, &zone),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy"},
			},
		},
	})
}

func TestAccAWSRoute53Zone_VPC_Updates(t *testing.T) {
	var zone route53.GetHostedZoneOutput

	rName := acctest.RandomWithPrefix("tf-acc-test")
	resourceName := "aws_route53_zone.test"
	vpcResourceName1 := "aws_vpc.test1"
	vpcResourceName2 := "aws_vpc.test2"
	zoneName := testAccRandomDomainName()

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		ErrorCheck:   testAccErrorCheck(t, route53.EndpointsID),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckRoute53ZoneDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53ZoneConfigVPCSingle(rName, zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "vpc.#", "1"),
					testAccCheckRoute53ZoneAssociatesWithVpc(vpcResourceName1, &zone),
				),
			},
			{
				Config: testAccRoute53ZoneConfigVPCMultiple(rName, zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "vpc.#", "2"),
					testAccCheckRoute53ZoneAssociatesWithVpc(vpcResourceName1, &zone),
					testAccCheckRoute53ZoneAssociatesWithVpc(vpcResourceName2, &zone),
				),
			},
			{
				Config: testAccRoute53ZoneConfigVPCSingle(rName, zoneName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53ZoneExists(resourceName, &zone),
					resource.TestCheckResourceAttr(resourceName, "vpc.#", "1"),
					testAccCheckRoute53ZoneAssociatesWithVpc(vpcResourceName1, &zone),
				),
			},
		},
	})
}

func testAccCheckRoute53ZoneDestroy(s *terraform.State) error {
	return testAccCheckRoute53ZoneDestroyWithProvider(s, testAccProvider)
}

func testAccCheckRoute53ZoneDestroyWithProvider(s *terraform.State, provider *schema.Provider) error {
	conn := provider.Meta().(*AWSClient).r53conn
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "aws_route53_zone" {
			continue
		}

		_, err := conn.GetHostedZone(&route53.GetHostedZoneInput{Id: aws.String(rs.Primary.ID)})
		if err == nil {
			return fmt.Errorf("Hosted zone still exists")
		}
	}
	return nil
}

func testAccCreateRandomRoute53RecordsInZoneId(zone *route53.GetHostedZoneOutput, recordsCount int) resource.TestCheckFunc {
	return testAccCreateRandomRoute53RecordsInZoneIdWithProvider(func() *schema.Provider { return testAccProvider }, zone, recordsCount)
}

func testAccCreateRandomRoute53RecordsInZoneIdWithProvider(providerF func() *schema.Provider, zone *route53.GetHostedZoneOutput, recordsCount int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		provider := providerF()
		conn := provider.Meta().(*AWSClient).r53conn

		var changes []*route53.Change
		if recordsCount > 100 {
			return fmt.Errorf("Route53 API only allows 100 record sets in a single batch")
		}
		for i := 0; i < recordsCount; i++ {
			changes = append(changes, &route53.Change{
				Action: aws.String("UPSERT"),
				ResourceRecordSet: &route53.ResourceRecordSet{
					Name: aws.String(fmt.Sprintf("%d-tf-acc-random.%s", acctest.RandInt(), *zone.HostedZone.Name)),
					Type: aws.String("CNAME"),
					ResourceRecords: []*route53.ResourceRecord{
						{Value: aws.String(fmt.Sprintf("random.%s", *zone.HostedZone.Name))},
					},
					TTL: aws.Int64(int64(30)),
				},
			})
		}

		req := &route53.ChangeResourceRecordSetsInput{
			HostedZoneId: zone.HostedZone.Id,
			ChangeBatch: &route53.ChangeBatch{
				Comment: aws.String("Generated by Terraform"),
				Changes: changes,
			},
		}
		log.Printf("[DEBUG] Change set: %s\n", *req)
		resp, err := changeRoute53RecordSet(conn, req)
		if err != nil {
			return err
		}
		changeInfo := resp.(*route53.ChangeResourceRecordSetsOutput).ChangeInfo
		err = waitForRoute53RecordSetToSync(conn, cleanChangeID(*changeInfo.Id))
		return err
	}
}

func testAccCheckRoute53ZoneExists(n string, zone *route53.GetHostedZoneOutput) resource.TestCheckFunc {
	return testAccCheckRoute53ZoneExistsWithProvider(n, zone, func() *schema.Provider { return testAccProvider })
}

func testAccCheckRoute53ZoneExistsWithProvider(n string, zone *route53.GetHostedZoneOutput, providerF func() *schema.Provider) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No hosted zone ID is set")
		}

		provider := providerF()
		conn := provider.Meta().(*AWSClient).r53conn
		resp, err := conn.GetHostedZone(&route53.GetHostedZoneInput{Id: aws.String(rs.Primary.ID)})
		if err != nil {
			return fmt.Errorf("Hosted zone err: %v", err)
		}

		aws_comment := *resp.HostedZone.Config.Comment
		rs_comment := rs.Primary.Attributes["comment"]
		if rs_comment != "" && rs_comment != aws_comment {
			return fmt.Errorf("Hosted zone with comment '%s' found but does not match '%s'", aws_comment, rs_comment)
		}

		if !*resp.HostedZone.Config.PrivateZone {
			sorted_ns := make([]string, len(resp.DelegationSet.NameServers))
			for i, ns := range resp.DelegationSet.NameServers {
				sorted_ns[i] = *ns
			}
			sort.Strings(sorted_ns)
			for idx, ns := range sorted_ns {
				attribute := fmt.Sprintf("name_servers.%d", idx)
				dsns := rs.Primary.Attributes[attribute]
				if dsns != ns {
					return fmt.Errorf("Got: %v for %v, Expected: %v", dsns, attribute, ns)
				}
			}
		}

		*zone = *resp
		return nil
	}
}

func testAccCheckRoute53ZoneDisappears(zone *route53.GetHostedZoneOutput) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		conn := testAccProvider.Meta().(*AWSClient).r53conn

		input := &route53.DeleteHostedZoneInput{
			Id: zone.HostedZone.Id,
		}

		_, err := conn.DeleteHostedZone(input)

		return err
	}
}

func testAccCheckRoute53ZoneAssociatesWithVpc(n string, zone *route53.GetHostedZoneOutput) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No VPC ID is set")
		}

		for _, vpc := range zone.VPCs {
			if aws.StringValue(vpc.VPCId) == rs.Primary.ID {
				return nil
			}
		}

		return fmt.Errorf("VPC: %s is not associated to Zone: %v", n, cleanZoneID(aws.StringValue(zone.HostedZone.Id)))
	}
}

func testAccCheckDomainName(zone *route53.GetHostedZoneOutput, domain string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if zone.HostedZone.Name == nil {
			return fmt.Errorf("Empty name in HostedZone for domain %s", domain)
		}

		if aws.StringValue(zone.HostedZone.Name) == domain {
			return nil
		}

		return fmt.Errorf("Invalid domain name. Expected %s is %s", domain, *zone.HostedZone.Name)
	}
}

func testAccRoute53ZoneConfig(zoneName string) string {
	return fmt.Sprintf(`
resource "aws_route53_zone" "test" {
  name = "%s."
}
`, zoneName)
}

func testAccRoute53ZoneConfigMultiple(domainName string) string {
	return fmt.Sprintf(`
resource "aws_route53_zone" "test" {
  count = 5

  name = "subdomain${count.index}.%[1]s"
}
`, domainName)
}

func testAccRoute53ZoneConfigComment(zoneName, comment string) string {
	return fmt.Sprintf(`
resource "aws_route53_zone" "test" {
  comment = %q
  name    = "%s."
}
`, comment, zoneName)
}

func testAccRoute53ZoneConfigDelegationSetID(zoneName string) string {
	return fmt.Sprintf(`
resource "aws_route53_delegation_set" "test" {}

resource "aws_route53_zone" "test" {
  delegation_set_id = aws_route53_delegation_set.test.id
  name              = "%s."
}
`, zoneName)
}

func testAccRoute53ZoneConfigForceDestroy(zoneName string) string {
	return fmt.Sprintf(`
resource "aws_route53_zone" "test" {
  force_destroy = true
  name          = "%s"
}
`, zoneName)
}

func testAccRoute53ZoneConfigForceDestroyTrailingPeriod(zoneName string) string {
	return fmt.Sprintf(`
resource "aws_route53_zone" "test" {
  force_destroy = true
  name          = "%s."
}
`, zoneName)
}

func testAccRoute53ZoneConfigTagsSingle(zoneName, tag1Key, tag1Value string) string {
	return fmt.Sprintf(`
resource "aws_route53_zone" "test" {
  name = "%s."

  tags = {
    %q = %q
  }
}
`, zoneName, tag1Key, tag1Value)
}

func testAccRoute53ZoneConfigTagsMultiple(zoneName, tag1Key, tag1Value, tag2Key, tag2Value string) string {
	return fmt.Sprintf(`
resource "aws_route53_zone" "test" {
  name = "%s."

  tags = {
    %q = %q
    %q = %q
  }
}
`, zoneName, tag1Key, tag1Value, tag2Key, tag2Value)
}

func testAccRoute53ZoneConfigVPCSingle(rName, zoneName string) string {
	return fmt.Sprintf(`
resource "aws_vpc" "test1" {
  cidr_block = "10.1.0.0/16"

  tags = {
    Name = %[1]q
  }
}

resource "aws_route53_zone" "test" {
  name = "%[2]s."

  vpc {
    vpc_id = aws_vpc.test1.id
  }
}
`, rName, zoneName)
}

func testAccRoute53ZoneConfigVPCMultiple(rName, zoneName string) string {
	return fmt.Sprintf(`
resource "aws_vpc" "test1" {
  cidr_block = "10.1.0.0/16"

  tags = {
    Name = %[1]q
  }
}

resource "aws_vpc" "test2" {
  cidr_block = "10.2.0.0/16"

  tags = {
    Name = %[1]q
  }
}

resource "aws_route53_zone" "test" {
  name = "%[2]s."

  vpc {
    vpc_id = aws_vpc.test1.id
  }

  vpc {
    vpc_id = aws_vpc.test2.id
  }
}
`, rName, zoneName)
}
