#!/usr/bin/env python3
"""Regression assertions for the reviewed Terraform source defaults (no apply)."""
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2] / 'infra' / 'terraform'


def block(source, heading):
    start = source.index(heading)
    start = source.index('{', start) + 1
    depth = 1
    for i in range(start, len(source)):
        depth += (source[i] == '{') - (source[i] == '}')
        if depth == 0:
            return source[start:i]
    raise AssertionError('unterminated Terraform block')


def verify(variables, main, aurora):
    public = block(variables, 'variable "cluster_endpoint_public_access"')
    assert re.search(r'^\s*default\s*=\s*false\s*$', public, re.M), 'EKS API must default private'
    eks = block(main, 'module "eks"')
    assert re.search(r'^\s*cluster_endpoint_public_access\s*=\s*var\.cluster_endpoint_public_access\s*$', eks, re.M), 'EKS must use the private default'
    egress = block(aurora, 'resource "aws_security_group_rule" "aurora_egress_vpc"')
    assert re.search(r'^\s*cidr_blocks\s*=\s*\[var\.vpc_cidr\]\s*$', egress, re.M), 'Aurora egress must stay VPC-scoped'
    assert not re.search(r'ipv6_cidr_blocks|source_security_group_id|prefix_list_ids', egress), 'unexpected additional egress destination'


class ReviewedDefaults(unittest.TestCase):
    def test_defaults_and_reversion_controls(self):
        variables, main, aurora = [(ROOT / name).read_text() for name in ('variables.tf', 'main.tf', 'aurora.tf')]
        verify(variables, main, aurora)
        changed = variables.replace(block(variables, 'variable "cluster_endpoint_public_access"'), '\n default = true\n')
        with self.assertRaisesRegex(AssertionError, 'private'):
            verify(changed, main, aurora)
        with self.assertRaisesRegex(AssertionError, 'VPC-scoped'):
            verify(variables, main, aurora.replace('cidr_blocks       = [var.vpc_cidr]', 'cidr_blocks = ["0.0.0.0/0"]'))


if __name__ == '__main__':
    unittest.main()
