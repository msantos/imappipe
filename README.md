# SYNOPSIS

imappipe [*options*] *server*:*port*

# DESCRIPTION

Poll an IMAP mailbox and write the messages to standard output.

# BUILDING

    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"

# EXAMPLES

    imappipe --username=user@example.com --password=example \
      mail.example.com:993

# OPTIONS

mailbox *string*
:   IMAP mailbox (default "INBOX")

password *string*
:   IMAP password

poll-timeout *duration*
:   Set poll interval if IDLE not supported

template *string*
:   message template

username *string*
:   IMAP username

verbose *int*
:   Enable debug messages

no-tls *bool*
:   Disable connecting to the IMAP port using TLS

# TEMPLATE

See the [default
template](https://github.com/msantos/imappipe/blob/master/template.txt).

A message consists of the following fields:

Date
:   The time in RFC3339 format when the message was retrieved.

Header
:   Message headers. See _Header_.

Body
:   The list of message bodies.

Attachment
:   The list of files attached to the message. See _Attachment_.

## Header

From
:   The list of "from" addresses.

To
:   The list of "to" addresses.

Date
:   The message header date.

Subject
:   The message header subject.

Map
:   A map containing all the message headers.

## Attachment

Name
:   File name

Content
:   File content

## Functions

### join

Concatenate an array of strings using the provided string.

```
From: {{ .Header.From | join ", " }}
```

### re

Match a message field using a regular expression.

```
{{ if re .Header.Subject "foo" -}}
{{- range $v := .Body }}
{{ $v }}
{{- end }}
{{- end }}
```

### strip

Remove any HTML elements from a message field.

```
{{ .Body | join "\n" | strip }}
```
