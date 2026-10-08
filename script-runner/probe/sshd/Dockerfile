# Git-Anbieter für die Docker-Sonden: sshd mit einem Nutzer „git“, der nur
# git-upload-pack ausführen kann. Das Bare-Repo und der öffentliche Schlüssel
# kommen zur Laufzeit (probe_test.go). NICHT ausliefern.
FROM alpine:3.22
RUN apk add --no-cache openssh-server git \
 && adduser -D -s /usr/bin/git-shell git \
 && sed -i 's/^git:!/git:*/' /etc/shadow \
 && mkdir -p /home/git/.ssh /srv \
 && chown -R git:git /home/git \
 && chmod 0700 /home/git/.ssh \
 && ssh-keygen -A \
 && printf 'PasswordAuthentication no\nPermitRootLogin no\nAllowUsers git\n' >> /etc/ssh/sshd_config
# Protokoll v2 und „want“ nach Hash, wie bei den großen Anbietern.
RUN git config --system uploadpack.allowAnySHA1InWant true
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
