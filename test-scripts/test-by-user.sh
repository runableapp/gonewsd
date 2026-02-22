#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# assum it's compiled and running: ./test/test.sh

cd "$(dirname "${BASH_SOURCE[0]}")/.."

gn='./bin/gonewsd -c ./test-scripts/test.conf'

# modify all gn to $gn

# Since in this script, gn='./bin/gonewsd -c ./test-scripts/test.conf'
# All subsequent invocations should change: gn ...  => $gn ...

$gn listuser --format pretty
$gn listgroup --format pretty

read -p "Press enter to continue..."

# ========================== group admin

# 🚀 setup initial test groups
$gn addgroup -group test.group1 -g rw -o r -desc "Test group 1" -creator "test" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
$gn addgroup -group test.group2 -g rw -o r -desc "Test group 2" -creator "test" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
$gn addgroup -group test.group3 -g rw -o r -desc "Test group 3" -creator "test" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
$gn addgroup -group test4 -g rw -o r -desc "Test group 4" -creator "test" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
$gn addgroup -group test-tmp -g rw -o r -desc "Temporary test" -creator "test" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"
$gn listgroup --format pretty

echo
echo "=== adding test2 group"
$gn addgroup -group test2 -g rw -o r -desc "Test 2" -creator "test" -postlimit 1000 -ccpost "-" -replyto "-" -voidemail "root"

$gn listgroup --format pretty

echo
echo "=== updating test4 group"
$gn updategroup -group test4 -g rw -o rw
$gn listgroup --format pretty

echo
echo "=== deleting test-tmp group"
$gn deletegroup -group test-tmp -y
$gn listgroup --format pretty

echo
echo "=== update group test, update any group here:"
$gn updategroup

read -p "Press enter to continue..."

# ========================== user admin

$gn adduser -user test@test.com -pass testpass12 -groups test.group1
$gn adduser -user test2@test.com -pass testpass12 -groups test.group2,test.group3
$gn adduser -user test3@test.com -pass testpass12 -groups test.group3

echo
echo "=== adding test4 user, give access to groups"
$gn listgroup --format pretty
$gn adduser -user test4@test.com -pass testpass12

# adding tmp user
$gn adduser -user test-tmp@test.com -pass testpass12 -groups test.group3

echo
echo "=== adding user, add any user"
$gn listgroup --format pretty
$gn adduser
$gn listuser --format pretty

read -p "Press enter to continue..."

echo 
echo "=== updating test4 user, give access to 'test4' group"
$gn updateuser -user test4@test.com -groups test4 -y
$gn listuser --format pretty
$gn updateuser

read -p "Press enter to continue..."

echo
echo "=== deleting test-tmp@test.com"
$gn deleteuser -user test-tmp@test.com -y

$gn listuser --format pretty
$gn listgroup --format pretty

echo
echo
echo "🎉 Done!"


