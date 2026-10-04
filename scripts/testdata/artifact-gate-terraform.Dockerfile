FROM docker.io/hashicorp/terraform:1.13.5 AS terraform
FROM docker.io/library/bash:5.2
COPY --from=terraform /bin/terraform /usr/local/bin/terraform
