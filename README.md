# AWS S3 Exporter

Situation/Problem: I want to verify that my backup tasks are running but I don't necessarily have the ability to instrument the backup application I'm using. My backups go to an S3 bucket. If I could alert when the last modified date is outside a known range, then I'd have a fairly good indiciation when something is wrong with my backups.

Solution: An exporter that can report the last modified date for objects in a bucket that match a given prefix.

This exporter queries the S3 API with a given bucket and prefix and constructs metrics based on the returned objects.