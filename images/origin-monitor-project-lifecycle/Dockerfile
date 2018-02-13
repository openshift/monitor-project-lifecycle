#
# The standard name for this image is openshift/monitor-project-lifecycle
#
FROM openshift/origin-base

RUN INSTALL_PKGS="origin-monitor-project-lifecycle" && \
    yum --enablerepo=origin-local-release install -y ${INSTALL_PKGS} && \
    rpm -V ${INSTALL_PKGS} && \
    yum clean all

LABEL io.k8s.display-name="OpenShift Project Lifecycle Monitor" \
      io.k8s.description="This is a component of OpenShift Container Platform and provides a monitoring component." \
      io.openshift.tags="openshift"

# doesn't require a root user.
USER 1001
EXPOSE 5000

CMD [ "/usr/bin/monitor" ]
