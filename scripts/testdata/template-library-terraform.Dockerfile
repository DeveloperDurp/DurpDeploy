# Reuse the Terraform version and Python runtime used by existing E2E fixtures.
FROM harbor.durp.info/dockerhub/hashicorp/terraform:1.13.5 AS terraform
FROM harbor.durp.info/dockerhub/library/python:3.12
COPY --from=terraform /bin/terraform /usr/local/bin/terraform
COPY main.tf .terraform.lock.hcl /opt/library-terraform/
USER 65534:65534
