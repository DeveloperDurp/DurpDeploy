FROM docker.io/hashicorp/terraform:1.13.5 AS terraform
ADD --checksum=sha256:7b8434212eef0f8c83f5a90c6d76feaf850f6502b61b53c329e85b3b281cba34 \
    --chmod=0644 \
    https://releases.hashicorp.com/terraform-provider-random/3.7.2/terraform-provider-random_3.7.2_linux_amd64.zip \
    /opt/providers/registry.terraform.io/hashicorp/random/terraform-provider-random_3.7.2_linux_amd64.zip
FROM docker.io/library/bash:5.2
COPY --from=terraform /bin/terraform /usr/local/bin/terraform
COPY --from=terraform /opt/providers /opt/providers
USER 65534:65534
