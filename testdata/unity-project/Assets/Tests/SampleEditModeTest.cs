using NUnit.Framework;

public class SampleEditModeTest
{
    [Test]
    public void PassingTest()
    {
        Assert.AreEqual(4, 2 + 2);
    }

    [Test]
    public void AnotherPassingTest()
    {
        Assert.IsTrue(true);
    }

    [TestCase(1, 2, 3)]
    [TestCase(0, 0, 0)]
    [TestCase(-1, 1, 0)]
    public void ParameterizedAddTest(int a, int b, int expected)
    {
        Assert.AreEqual(expected, a + b);
    }
}
